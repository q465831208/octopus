#!/usr/bin/env python3
"""Octopus 模型可用性测试 & 分组池同步工具（请求级负载均衡配套）。

工作流:
  1. `test <model>`  选择模型, 测试 Octopus 里哪些渠道声明的该模型真实可用（只报告, 不改分组）
  2. 手动把测试通过的渠道/模型加进 Octopus 分组（Web UI: 分组管理 -> 添加渠道模型）
  3. 分组内多个渠道/模型后, Octopus 会对该分组的请求做请求级轮询负载均衡,
     失败请求自动 failover 到下一个可用渠道（需要加载了负载均衡补丁的版本）

可选自动化: `sync <model>` 自动把可用项加入分组, 把失效项移出分组。

用法:
  python octopus_pool_manager.py test deepseek-v4-flash
  python octopus_pool_manager.py test deepseek-v4-flash --timeout 35
  python octopus_pool_manager.py sync deepseek-v4-flash

配置: 默认读取 ./pool-config.json, 可用 --config 指定。见 config.example.json。
"""
import argparse
import http.cookiejar
import json
import sqlite3
import sys
import time
import urllib.error
import urllib.request

DEFAULT_CONFIG = 'pool-config.json'


def load_config(path):
    with open(path, 'r', encoding='utf-8') as f:
        return json.load(f)


class OctopusAdmin:
    def __init__(self, base_url, username, password):
        self.base_url = base_url.rstrip('/')
        self.cj = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cj))
        self.login(username, password)

    def request(self, method, path, data=None, timeout=30):
        body = json.dumps(data, ensure_ascii=False).encode('utf-8') if data is not None else None
        req = urllib.request.Request(self.base_url + path, data=body,
                                     headers={'Content-Type': 'application/json'}, method=method)
        try:
            with self.opener.open(req, timeout=timeout) as r:
                raw = r.read().decode('utf-8', 'replace')
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as e:
            raw = e.read().decode('utf-8', 'replace')
            try:
                return json.loads(raw)
            except Exception:
                return {'code': e.code, 'message': raw}

    def login(self, username, password):
        resp = self.request('POST', '/api/v1/user/login', {'username': username, 'password': password})
        if resp.get('code') != 200:
            raise RuntimeError('Octopus admin login failed: %s' % resp)

    def groups(self):
        resp = self.request('GET', '/api/v1/group/list')
        if resp.get('code') != 200:
            raise RuntimeError('group list failed: %s' % resp)
        return resp.get('data') or []

    def ensure_group(self, name, retry_interval=1):
        for g in self.groups():
            if g.get('name') == name:
                return g
        resp = self.request('POST', '/api/v1/group/create', {'name': name, 'retry_interval': retry_interval})
        if resp.get('code') != 200:
            raise RuntimeError('group create failed: %s' % resp)
        return resp.get('data')

    def update_group_items(self, group_id, add_items, delete_item_ids):
        payload = {'id': group_id}
        if add_items:
            payload['items_to_add'] = add_items
        if delete_item_ids:
            payload['items_to_delete'] = delete_item_ids
        if len(payload) == 1:
            return None
        resp = self.request('POST', '/api/v1/group/update', payload)
        if resp.get('code') != 200:
            raise RuntimeError('group update failed: %s' % resp)
        return resp.get('data')


def normalize_base_url(base_url, channel_type):
    b = (base_url or '').rstrip('/')
    if not b:
        return ''
    if channel_type in ('openai', 'openai_responses'):
        return b + '/chat/completions'
    if channel_type == 'anthropic':
        return b + '/messages'
    return b


def split_models(s):
    return [x.strip() for x in (s or '').replace('\n', ',').split(',') if x.strip()]


def channel_supports_model(channel, model, aliases):
    models = split_models(channel.get('model')) + split_models(channel.get('custom_model'))
    targets = set([model] + aliases)
    return [m for m in models if m in targets]


def read_channels(db_path):
    con = sqlite3.connect(db_path)
    con.row_factory = sqlite3.Row
    rows = con.execute(
        'select id,name,type,enabled,base_url,key,model,custom_model,custom_header,param_override '
        'from channels order by id').fetchall()
    con.close()
    return [dict(r) for r in rows]


def parse_custom_header(raw):
    if not raw:
        return {}
    try:
        arr = json.loads(raw)
    except Exception:
        return {}
    headers = {}
    if isinstance(arr, list):
        for h in arr:
            if isinstance(h, dict):
                k = h.get('header_key') or h.get('headerKey') or h.get('key')
                v = h.get('header_value') or h.get('headerValue') or h.get('value')
                if k and v:
                    headers[str(k)] = str(v)
    return headers


def test_channel(channel, actual_model, timeout=35):
    ctype = channel.get('type') or 'openai'
    url = normalize_base_url(channel.get('base_url'), ctype)
    key = channel.get('key') or ''
    if not url or not key:
        return False, 0, 'missing base_url/key'

    headers = {'Content-Type': 'application/json'}
    headers.update(parse_custom_header(channel.get('custom_header')))

    if ctype == 'anthropic':
        headers['x-api-key'] = key
        headers.setdefault('anthropic-version', '2023-06-01')
        payload = {'model': actual_model, 'max_tokens': 8,
                   'messages': [{'role': 'user', 'content': 'ping'}]}
    else:
        headers['Authorization'] = 'Bearer ' + key
        payload = {'model': actual_model, 'messages': [{'role': 'user', 'content': 'ping'}],
                   'max_tokens': 8, 'stream': False}

    data = json.dumps(payload, ensure_ascii=False).encode('utf-8')
    req = urllib.request.Request(url, data=data, headers=headers, method='POST')
    start = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            body = r.read(2048).decode('utf-8', 'replace')
            elapsed = int((time.time() - start) * 1000)
            if 200 <= r.status < 300:
                return True, elapsed, 'ok'
            return False, elapsed, 'http_%s %s' % (r.status, body[:200])
    except urllib.error.HTTPError as e:
        elapsed = int((time.time() - start) * 1000)
        body = e.read(512).decode('utf-8', 'replace')
        return False, elapsed, 'http_%s %s' % (e.code, body[:200].replace('\n', ' '))
    except Exception as e:
        elapsed = int((time.time() - start) * 1000)
        return False, elapsed, str(e)[:240]


def run_test(cfg, model, timeout):
    aliases = cfg.get('model_aliases', {}).get(model, [])
    channels = read_channels(cfg['octopus_db'])
    candidates = []
    for ch in channels:
        if not ch.get('enabled'):
            continue
        for actual_model in channel_supports_model(ch, model, aliases):
            candidates.append((ch, actual_model))

    print('MODEL   :', model)
    print('ALIASES :', ','.join(aliases) if aliases else '-')
    print('CANDIDATES:', len(candidates))
    print('-' * 100)

    healthy, failed = [], []
    for ch, actual_model in candidates:
        ok, ms, msg = test_channel(ch, actual_model, timeout=timeout)
        tag = '[可用]' if ok else '[不可用]'
        print('%-8s %-46s %-32s %7dms  %s' % (tag, ch['name'], actual_model, ms, msg))
        if ok:
            healthy.append({'channel_id': ch['id'], 'model_name': actual_model,
                            'channel_name': ch['name'], 'latency_ms': ms})
        else:
            failed.append({'channel_id': ch['id'], 'model_name': actual_model,
                           'channel_name': ch['name'], 'error': msg})

    print('-' * 100)
    print('SUMMARY: 可用=%d  不可用=%d' % (len(healthy), len(failed)))
    if healthy:
        print('可手动加入分组 %s 的项(按延迟排序):' % model)
        for h in sorted(healthy, key=lambda x: (x['latency_ms'], x['channel_id'])):
            print('  - channel_id=%d  model=%s  (%s, %dms)'
                  % (h['channel_id'], h['model_name'], h['channel_name'], h['latency_ms']))
    return healthy, failed


def cmd_test(args):
    cfg = load_config(args.config)
    run_test(cfg, args.model, args.timeout)


def cmd_sync(args):
    cfg = load_config(args.config)
    model = args.model
    healthy, _ = run_test(cfg, model, args.timeout)

    admin = OctopusAdmin(cfg['octopus_base_url'], cfg['admin_username'], cfg['admin_password'])
    group = admin.ensure_group(model, retry_interval=cfg.get('retry_interval', 1))
    groups = admin.groups()
    group = next(g for g in groups if g.get('name') == model)
    items = group.get('items') or []

    healthy_keys = set((h['channel_id'], h['model_name']) for h in healthy)
    existing_keys = set((it.get('channel_id'), it.get('model_name')) for it in items)

    add_items, prio = [], 1
    for h in sorted(healthy, key=lambda x: (x['latency_ms'], x['channel_id'], x['model_name'])):
        if (h['channel_id'], h['model_name']) not in existing_keys:
            add_items.append({'channel_id': h['channel_id'], 'model_name': h['model_name'], 'priority': prio})
        prio += 1

    delete_ids = [it.get('id') for it in items
                  if (it.get('channel_id'), it.get('model_name')) not in healthy_keys]

    if not args.dry_run:
        admin.update_group_items(group['id'], add_items, delete_ids)
    print('SYNC SUMMARY: healthy=%d add=%d delete=%d dry_run=%s'
          % (len(healthy), len(add_items), len(delete_ids), args.dry_run))


def main():
    p = argparse.ArgumentParser(description='Octopus 模型可用性测试与分组池同步')
    p.add_argument('--config', default=DEFAULT_CONFIG)
    sub = p.add_subparsers(dest='cmd', required=True)

    s = sub.add_parser('test', help='测试指定模型在哪些渠道可用（只报告, 不改分组）')
    s.add_argument('model')
    s.add_argument('--timeout', type=int, default=35)

    y = sub.add_parser('sync', help='测试并把可用项自动同步进分组（可用才进池, 失效自动移出）')
    y.add_argument('model')
    y.add_argument('--timeout', type=int, default=35)
    y.add_argument('--dry-run', action='store_true')

    args = p.parse_args()
    if args.cmd == 'test':
        cmd_test(args)
    elif args.cmd == 'sync':
        cmd_sync(args)


if __name__ == '__main__':
    sys.exit(main() or 0)
