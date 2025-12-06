# ---------- build ----------
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/webcli ./cmd/webcli
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/gateway ./cmd/gateway
RUN GOBIN=/out go install github.com/sorenisanerd/gotty@v1.6.0

FROM alpine:3.20
RUN apk add --no-cache tcpdump

RUN apk add --no-cache \
    ca-certificates tzdata iputils coreutils util-linux \
    python3 py3-pip py3-virtualenv openssh-client sshpass

RUN python3 -m virtualenv /opt/ansible && \
    PIP_DISABLE_PIP_VERSION_CHECK=1 /opt/ansible/bin/pip install --no-cache-dir \
      'urllib3<2' 'requests<2.32' 'ansible-core==2.14.*' && \
    ln -sf /opt/ansible/bin/ansible /usr/bin/ansible && \
    ln -sf /opt/ansible/bin/ansible-playbook /usr/bin/ansible-playbook

ENV PATH="/opt/ansible/bin:${PATH}" \
    ANSIBLE_COLLECTIONS_PATH="/usr/share/ansible/collections:/root/.ansible/collections" \
    ANSIBLE_LIBRARY="/usr/local/lib/python3.9/dist-packages/ansible_collections/community/docker"

COPY --from=build /out/webcli  /app/webcli
COPY --from=build /out/gateway /app/gateway
COPY --from=build /out/gotty   /usr/local/bin/gotty

RUN adduser -D -H -s /sbin/nologin -u 1000 webcli && \
    mkdir -p /app/logs && chown -R webcli:webcli /app
USER webcli
WORKDIR /app


ENV GATEWAY_ADDR=":8080" \
    GOTTY_PORT_INTERNAL="8081" \
    WEBCLI_BIN="/app/webcli"

EXPOSE 8080

ENTRYPOINT ["/app/gateway"]
