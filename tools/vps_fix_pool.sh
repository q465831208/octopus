#!/bin/bash
# ============================================================
# VPS 数据修复: 渠道 base_url 补 /v1 + 重建 deepseek-v4-flash 分组 + 测试收敛
# 适用: 旧数据卷(/opt/octopus/data)里的渠道 base_url 缺少 /v1,
#       且分组池被启动健康任务清空/模型列表被旧同步任务清掉的情况。
# 在 VPS 上以 root 运行: bash vps_fix_pool.sh
# ============================================================
set -euo pipefail

DATA_DB="${DATA_DB:-/opt/octopus/data/data.db}"
OCTO_URL="${OCTO_URL:-http://127.0.0.1:7070}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-39497981}"
GROUP_NAME="${GROUP_NAME:-deepseek-v4-flash}"

echo "==> 1/5 停服务（避免 WAL 写锁）"
docker stop octopus

echo "==> 2/5 修正渠道 base_url（补 /v1）并清掉可能残留的 demo 分组"
python3 - <<PY
import sqlite3
con = sqlite3.connect("$DATA_DB")
c = con.cursor()
c.execute("""UPDATE channels SET base_url = rtrim(base_url,'/') || '/v1'
             WHERE type IN ('openai','openai_responses','anthropic')
               AND base_url IS NOT NULL AND base_url <> ''
               AND rtrim(base_url,'/') NOT LIKE '%/v1'""")
print('  base_url 补 /v1 渠道数:', c.rowcount)
# 删除残留的分组项与 demo 分组（演示用分组不需要）
c.execute("DELETE FROM group_items WHERE group_id IN (SELECT id FROM groups WHERE name='demo')")
c.execute("DELETE FROM groups WHERE name='demo'")
con.commit(); con.close()
print('  DB 修改完成')
PY

echo "==> 3/5 启动 octopus"
docker start octopus
sleep 5
curl -sf -o /dev/null "http://127.0.0.1:7070/" || { echo 'octopus 未就绪'; exit 1; }
echo "  octopus 已启动"

echo "==> 4/5 触达模型同步（用修正后的 /v1 路径重新拉取各渠道模型列表，可能需几分钟）"
python3 - <<PY
import http.cookiejar, json, urllib.request

O="$OCTO_URL"
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

# 重建分组项
chans = req('GET','/api/v1/channel/list')['data']
g = next((x for x in req('GET','/api/v1/group/list')['data'] if x['name']=='$GROUP_NAME'), None)
gid = g['id'] if g else req('POST','/api/v1/group/create',{'name':'$GROUP_NAME','retry_interval':1}).get('data',{}).get('id')
to_add, prio = [], 1
for ch in chans:
    for m in ((ch.get('model') or '') + ',' + (ch.get('custom_model') or '')).split(','):
        m = m.strip()
        if not m: continue
        if m.split('/')[-1].lower().startswith('deepseek-v4-flash'):
            to_add.append({'channel_id': ch['id'], 'model_name': m, 'priority': prio}); prio += 1
print('  待加入 '$GROUP_NAME' 分组项:', len(to_add))
if to_add:
    r = req('POST','/api/v1/group/update', {'id': gid, 'items_to_add': to_add}, t=60)
    print('  分组更新:', r.get('code'), r.get('message'))
PY

echo "==> 5/5 一次性测试收敛（失效项自动移出，只留可用项负载均衡）"
python3 - <<PY
import http.cookiejar, json, urllib.request, time
O="$OCTO_URL"
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
t0=time.time()
r = req('POST','/api/v1/group/test-all', {'group_id': g['id']}, t=900)
d = r.get('data') or {}
print(f"  测试完成: tested={d.get('tested')} kept={d.get('kept')} removed={d.get('removed')} ({int(time.time()-t0)}s)")
for res in d.get('results', []):
    if res.get('ok'):
        print('    ✔', res.get('model_name'), f"{res.get('latency_ms')}ms")
    else:
        print('    ✘', res.get('model_name'), str(res.get('detail'))[:60])
PY

echo "✅ 完成。WebUI 打开 http://217.142.187.171:7070 (admin/$ADMIN_PASS) 验证分组页。"