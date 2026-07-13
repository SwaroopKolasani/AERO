# AeroCore Local Ops

AeroCore is the local placement/control-plane service for Project Aero.

It exposes:

- `GET /healthz`
- `GET /readyz`
- `GET /config`
- `GET /metrics`
- `GET /backends`
- `PUT /backends/{id}`
- `PATCH /backends/{id}/health`
- `DELETE /backends/{id}`
- `POST /resolve`

## Run locally

```bash
AEROCORE_DEFAULT_UPSTREAM_URL=http://localhost:11434 \
AEROCORE_BACKENDS_FILE=configs/backends.example.json \
go run ./aerocore