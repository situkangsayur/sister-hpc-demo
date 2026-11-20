#!/bin/sh
# Isi template route dengan kunci dari env APISIX_KEY, lalu jalankan entrypoint bawaan image.
set -e
sed "s|__APISIX_KEY__|${APISIX_KEY}|" /tpl/apisix.yaml.tpl > /usr/local/apisix/conf/apisix.yaml
exec /docker-entrypoint.sh docker-start
