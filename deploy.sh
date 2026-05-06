#!/bin/bash
set -e
cd "$(dirname "$0")"

case "${1:-up}" in
  up)
    N="${2:-1}"
    echo "[*] Building worker image ..."
    docker compose build worker 2>/dev/null || docker build -t ptagent-worker:latest -f Dockerfile.worker .
    echo "[*] Starting server + $N dispatcher(s) ..."
    docker compose up -d --build --scale dispatcher=$N
    echo "[+] Done! Web UI: http://localhost:8000"
    ;;
  down)
    docker compose down
    ;;
  logs)
    docker compose logs -f ${@:2}
    ;;
  scale)
    N="${2:-3}"
    docker compose up -d --scale dispatcher=$N
    ;;
  *)
    echo "Usage: $0 {up [N]|down|logs|scale N}"
    ;;
esac