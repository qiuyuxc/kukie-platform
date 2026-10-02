#!/bin/sh
# 探测部署机所在地区，为 .env 选择更快的镜像源。
#
# 在 deploy/ 目录下运行：
#   sh detect-region.sh          探测并更新 .env
#   sh detect-region.sh --print  只打印镜像地址，不写文件
#
# 判定依据有两条，按顺序尝试：
#   1. 出口 IP 归属地：myip.ipip.net（国内接口，回退 ipinfo.io/country）
#   2. 归属地探测失败时，比较两个源的实际连通耗时，取更快的一个
# 选定后还会确认该源可用，不可用则回退直连。
#
# 归属地判断不准时，可用 KUKIE_REGION=cn 或 KUKIE_REGION=global 强制指定。
set -eu

cd "$(dirname "$0")"

print_only=0
if [ "${1:-}" = "--print" ]; then
  print_only=1
fi

path=${KUKIE_IMAGE_PATH:-qiuyuxc/kukie-platform}
tag=${KUKIE_IMAGE_TAG:-latest}
cn_mirror=${KUKIE_GHCR_MIRROR:-ghcr.nju.edu.cn}
upstream=ghcr.io

if ! command -v curl >/dev/null 2>&1; then
  echo '需要 curl 才能探测。请手动把 .env 中的 KUKIE_IMAGE 改成合适的镜像地址。' >&2
  exit 1
fi

country() {
  text=$(curl -fsS --max-time 6 https://myip.ipip.net 2>/dev/null || true)
  case "$text" in
    *中国*) echo CN; return ;;
  esac
  code=$(curl -fsS --max-time 6 https://ipinfo.io/country 2>/dev/null | tr -d '[:space:]' || true)
  case "$code" in
    CN) echo CN ;;
    [A-Z][A-Z]) echo OTHER ;;
    *) echo UNKNOWN ;;
  esac
}

latency() {
  curl -s -o /dev/null --max-time 6 -w '%{time_total}' "https://$1/v2/" 2>/dev/null || echo 999
}

reachable() {
  curl -fsS --max-time 8 -o /dev/null "https://$1/v2/" 2>/dev/null
}

region=$(printf '%s' "${KUKIE_REGION:-}" | tr '[:upper:]' '[:lower:]')
case "$region" in
  cn) region=CN ;;
  global|other) region=OTHER ;;
  '') region=$(country) ;;
  *) echo "KUKIE_REGION 只接受 cn 或 global，收到 $region" >&2; exit 1 ;;
esac
if [ "$region" = UNKNOWN ]; then
  cn_time=$(latency "$cn_mirror")
  up_time=$(latency "$upstream")
  echo "归属地探测失败，改用连通耗时比较：$cn_mirror ${cn_time}s / $upstream ${up_time}s" >&2
  region=$(awk -v a="$cn_time" -v b="$up_time" 'BEGIN { print (a <= b) ? "CN" : "OTHER" }')
fi

image="$upstream/$path:$tag"
if [ "$region" = CN ]; then
  if reachable "$cn_mirror"; then
    image="$cn_mirror/$path:$tag"
  else
    echo "国内加速源 $cn_mirror 当前不可用，回退直连 $upstream。" >&2
  fi
fi

if [ "$print_only" = 1 ]; then
  echo "$image"
  exit 0
fi

if [ ! -f .env ]; then
  cp .env.example .env
  chmod 600 .env
  echo '已从 .env.example 创建 .env，请确认 HTTPS/HTTP 与端口配置后再启动，无需填写站点域名。' >&2
fi

if grep -q '^KUKIE_IMAGE=' .env; then
  temporary=$(mktemp)
  sed "s|^KUKIE_IMAGE=.*|KUKIE_IMAGE=$image|" .env > "$temporary"
  cat "$temporary" > .env
  rm -f "$temporary"
else
  printf 'KUKIE_IMAGE=%s\n' "$image" >> .env
fi

echo "$image"
