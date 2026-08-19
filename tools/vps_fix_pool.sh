#!/bin/bash
# ============================================================
# VPS 分组池修复（不修改任何 base_url）
# 背景: 上游库对 OpenAI/Anthropic 已自动补全 /v1，base_url 无需改动。
#       自定义 URL 用 base_url 末尾 # (跳过版本补全) / ## (完整URL原样) 表达。
# 本脚本只做: 同步模型 -> 重建 deepseek-v4-flash 分组 -> 测试收敛(失效移出)。
# 在 VPS 上以 root 运行: bash vps_fix_pool.sh
# ============================================================
set -euo pipefail

OCTO_URL="${OCTO_URL:-http://127.0.0.1:7070}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-39497981}"
GROUP_NAME="${GROUP_NAME:-deepseek-v4-flash}"

echo "==> 确认 octopus 在线"
curl -sf -o /dev/null "$OCTO_URL/" || { echo "octopus 未就绪: $OCTO_URL"; exit 1; }

echo "==> 1/4 触达模型同步（重新拉取各渠道模型列表，可能需几分钟）"
python3 - <<PY
import http.cookiejar, json, urllib.request

O = "$OCTO_URL"
cj = http.cookiejar.CookieJar(); op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
def req(m, p, d=None, t=120):
    b = json.dumps(d).encode() if d is not None else None
    r = urllib.request.Request(O+p, data=b, headers={'Content-Type':'application/json'}, method=m)
    try:
        with op.open(r, timeout=t) as x: return json.loads(x.read().decode() or '{}')
    except Exception as e: return {'err': str(e)[:80]}

if req('POST','/api/v1/user/login',{'username':'$ADMIN_USER','password':'$ADMIN_PASS'}).get('code') != 200:
    print('登录失败'); raise SystemExit(1)
r = req('POST','/api/v1/channel/sync', t=900)
print('  模型同步:', r.get('code'), r.get('message'))

print('==> 2/4 重建 '$GROUP_NAME' 分组（按当前渠道模型列表）')
chans = req('GET','/api/v1/channel/list')['data']
g = next((x for x in req('GET','/api/v1/group/list')['data'] if x['name']=='$GROUP_NAME'), None)
gid = g['id'] if g else req('POST','/api/v1/group/create',{'name':'$GROUP_NAME','retry_interval':1}).get('data',{}).get('id')
# 清空旧项
if g and g.get('items'):
    ids = [it['id'] for it in g['items']]
    req('POST','/api/v1/group/update', {'id': gid, 'items_to_delete': ids}, t=60)
to_add, prio = [], 1
for ch in chans:
    for m in ((ch.get('model') or '') + ',' + (ch.get('custom_model') or '')).split(','):
        m = m.strip()
        if not m: continue
        if m.split('/')[-1].lower().startswith('deepseek-v4-flash'):
            to_add.append({'channel_id': ch['id'], 'model_name': m, 'priority': prio}); prio += 1
print('  待加入 '$GROUP_NAME' 项:', len(to_add))
if to_add:
    r = req('POST','/api/v1/group/update', {'id': gid, 'items_to_add': to_add}, t=60)
    print('  分组更新:', r.get('code'), r.get('message'))
PY

echo "==> 3/4 一次性测试收敛（失效项自动移出，只留可用项参与负载均衡）"
python3 - <<PY
import http.cookiejar, json, urllib.request, time
O = "$OCTO_URL"
cj = http.cookiejar.CookieJar(); op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
def req(m, p, d=None, t=120):
    b = json.dumps(d).encode() if d is not None else None
    r = urllib.request.Request(O+p, data=b, headers={'Content-Type':'application/json'}, method=m)
    try:
        with op.open(r, timeout=t) as x: return json.loads(x.read().decode() or '{}')
    except Exception as e: return {'err': str(e)[:80]}
req('POST','/api/v1/user/login',{'username':'$ADMIN_USER','password':'$ADMIN_PASS'})
g = next((x for x in req('GET','/api/v1/group/list')['data'] if x['name']=='$GROUP_NAME'), None)
if not g: print('分组不存在'); raise SystemExit(1)
t0 = time.time()
r = req('POST','/api/v1/group/test-all', {'group_id': g['id']}, t=900)
d = r.get('data') or {}
print(f"  测试完成: tested={d.get('tested')} kept={d.get('kept')} removed={d.get('removed')} ({int(time.time()-t0)}s)")
for res in d.get('results', []):
    if res.get('ok'):
        print('    OK', res.get('model_name'), f"{res.get('latency_ms')}ms")
    else:
        print('    XX', res.get('model_name'), str(res.get('detail'))[:60])
PY

echo "==> 4/4 完毕。若某渠道是自定义路径(非默认 /v1)，在该渠道 base_url 末尾加 # 或 ## 后重跑本脚本。"
echo "✅ 完成。WebUI http://217.142.187.171:7070 (admin/$ADMIN_PASS) 查看分组页。"