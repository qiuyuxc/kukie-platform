#!/bin/bash
set -euo pipefail
image=${1:?Pass the built image name}
test "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" = linux/amd64
name="kukie-smoke-$$"
volume="$name-data"
cleanup() {
  docker logs "$name" 2>/dev/null || true
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker volume create "$volume" >/dev/null
docker run -d --name "$name" --read-only --tmpfs /tmp --cap-drop ALL \
  --security-opt no-new-privileges:true -v "$volume:/data" "$image" >/dev/null
for attempt in $(seq 1 90); do
  if [ "$(docker inspect --format '{{.State.Health.Status}}' "$name")" = healthy ]; then break; fi
  if [ "$attempt" = 90 ]; then echo 'Runtime did not become healthy' >&2; exit 1; fi
  sleep 2
done
docker exec "$name" sh -ec '
  test "$(id -u)" = 10001
  test -s /data/master.key
  test -s /data/setup-token
  test -s /data/app.db
  ! command -v node
  ! command -v hugo
  curl -fsS http://127.0.0.1:8086/ | grep -q "<!DOCTYPE html>"
  test "$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8086/api/v1/admin/posts)" = 404
  test "$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8086/_server/site.json)" = 404
  curl -fsS http://127.0.0.1:8086/api/v1/config | grep -q registration_enabled
  curl -fsS http://127.0.0.1:8085/ | grep -q console-assets
  curl -fsS http://127.0.0.1:8084/api/v1/posts | grep -q posts
  test "$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8086/api/v1/me/bookmarks)" = 404
  for host in first.example.test second.example.test; do
    for path in / /about/ /links/ /sitemap.xml /index.xml /robots.txt; do
      page=$(curl -fsS -H "Host: $host" "http://127.0.0.1:8086$path")
      printf "%s" "$page" | grep -Fq "https://$host/"
      ! printf "%s" "$page" | grep -Fq "https://kukie.invalid/"
    done
  done
  test "$(curl -s -o /dev/null -w "%{http_code}" -X OPTIONS -H "Host: console.example.test" -H "Origin: https://console.example.test" http://127.0.0.1:8085/api/v1/auth/login)" = 204
  test "$(curl -s -o /dev/null -w "%{http_code}" -X OPTIONS -H "Host: console.example.test" -H "Origin: https://evil.example.test" http://127.0.0.1:8085/api/v1/auth/login)" = 403
'
before=$(docker exec "$name" sha256sum /data/master.key)
docker restart "$name" >/dev/null
for attempt in $(seq 1 60); do
  if docker exec "$name" curl -fsS http://127.0.0.1:8084/api/v1/health >/dev/null 2>&1; then break; fi
  if [ "$attempt" = 60 ]; then exit 1; fi
  sleep 2
done
test "$before" = "$(docker exec "$name" sha256sum /data/master.key)"
docker rm -f "$name" >/dev/null
docker run -d --name "$name" --read-only --tmpfs /tmp --cap-drop ALL \
  --security-opt no-new-privileges:true -v "$volume:/data" \
  -e KUKIE_SITE_URL=https://relocated.example.test "$image" >/dev/null
for attempt in $(seq 1 60); do
  if [ "$(docker inspect --format '{{.State.Health.Status}}' "$name")" = healthy ]; then break; fi
  if [ "$attempt" = 60 ]; then exit 1; fi
  sleep 2
done
docker exec "$name" sh -ec '
  for path in / /about/ /links/ /sitemap.xml /index.xml /robots.txt; do
    page=$(curl -fsS -H "Host: another.example.test" "http://127.0.0.1:8086$path")
    printf "%s" "$page" | grep -Fq "https://relocated.example.test/"
    ! printf "%s" "$page" | grep -Fq "https://kukie.invalid/"
  done
'
test "$before" = "$(docker exec "$name" sha256sum /data/master.key)"
echo 'Runtime smoke, restart, and runtime origin change passed'
