
---

## 4. `docs/troubleshooting.md`

```md id="xrdp8f"
# Troubleshooting

## `X-Aero-Cache: bypass`

Check bypass reason:

```bash
curl -s -D - -o /dev/null http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"llama3.2:3b","temperature":0,"messages":[{"role":"user","content":"hello"}]}'