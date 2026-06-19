## Claude Channel Affinity Override

### Problem

On production deployments, native Claude CLI requests to `/v1/messages?beta=true` can fail with:

```text
prompt_cache_key: Extra inputs are not permitted
```

This can happen even after the binary is updated with a fix.

### Root Cause

`new-api` loads config from two layers:

1. code defaults registered in `setting/*`
2. database-backed overrides from the `options` table

For channel affinity, the effective config is not only determined by
`setting/operation_setting/channel_affinity_setting.go`.
If the database contains keys like:

```text
channel_affinity_setting.rules
```

those values override the code defaults at runtime.

In our production case, the database still contained an older
`claude cli trace` rule that:

- matched both `^claude-.*$` and `^gpt-.*$`
- applied on `/v1/messages`
- synced `context:channel_affinity.key` into:
  - `header:session_id`
  - `json:prompt_cache_key`

That older DB rule caused native Claude upstream requests to include
`prompt_cache_key`, which `fm-claude` rejected.

### Expected Rule Split

Native Claude and Claude-to-GPT compatibility must be separated.

Recommended effective rules:

1. `codex cli trace`
   - models: `^gpt-.*$`
   - path: `/v1/responses`
   - source: `prompt_cache_key`
   - template: pass Codex headers only

2. `claude cli trace`
   - models: `^claude-.*$`
   - path: `/v1/messages`
   - sources:
     - `X-Claude-Code-Session-Id`
     - `metadata.user_id.session_id`
   - template: pass Claude headers only
   - must NOT write `json:prompt_cache_key`

3. `claude cli codex compat trace`
   - models: `^gpt-.*$`
   - path: `/v1/messages`
   - sources:
     - `X-Claude-Code-Session-Id`
     - `metadata.user_id.session_id`
   - template:
     - pass Claude headers
     - sync to `header:session_id`
     - sync to `json:prompt_cache_key`

### How To Check Production

Check whether DB overrides exist:

```bash
python3 - <<'PY'
import sqlite3
conn = sqlite3.connect('/home/Flames/new-api/data/new-api.db')
cur = conn.cursor()
cur.execute("select key, value from options where key like 'channel_affinity_setting.%' order by key")
for row in cur.fetchall():
    print(row[0])
    print(row[1])
    print('---')
PY
```

If `channel_affinity_setting.rules` exists, the database is overriding the binary defaults.

### Operational Note

When fixing this issue in production, updating the binary alone is insufficient if the DB override still exists.

You must either:

- update `options.key = 'channel_affinity_setting.rules'` to the new split rules
- or remove that row so runtime falls back to code defaults

Then restart `new-api.service`.
