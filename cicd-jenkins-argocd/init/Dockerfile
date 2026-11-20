# Image init: alpine + curl/jq/git/htpasswd + kubectl (biner k3s multicall dari image k3s yang
# juga dipakai service k3s, jadi tidak ada unduhan tambahan).
FROM rancher/k3s:v1.32.3-k3s1 AS k3s

FROM alpine:3.20
RUN apk add --no-cache bash curl jq git apache2-utils ca-certificates
COPY --from=k3s /bin/k3s /usr/local/bin/k3s
RUN ln -s k3s /usr/local/bin/kubectl
COPY init/init.sh /init.sh
COPY init/argocd /argocd
COPY seed /seed
ENTRYPOINT ["bash", "/init.sh"]
