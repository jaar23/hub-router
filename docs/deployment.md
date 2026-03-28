# Deployment

---

## Docker (Recommended)

### Build the image

```bash
docker build -t hub-router:latest -f deploy/Dockerfile .
# or using make:
make docker-build
```

### Run with environment variables

```bash
docker run -d \
  --name hub-router \
  -p 8080:8080 \
  -e HR_ONLINE_API_KEYS=your-online-key \
  -e HR_LOCAL_API_KEYS=your-local-key \
  -e HR_ADMIN_API_KEY=your-admin-key \
  hub-router:latest
```

### Run with an env file

```bash
cp deploy/.env.example deploy/.env
# edit deploy/.env
docker run -d --name hub-router -p 8080:8080 --env-file deploy/.env hub-router:latest
```

---

## Docker Compose

The provided `deploy/docker-compose.yml` is production-hardened:

```bash
cp deploy/.env.example deploy/.env
# Edit deploy/.env — set HR_ONLINE_API_KEYS and HR_LOCAL_API_KEYS at minimum

docker-compose -f deploy/docker-compose.yml up -d
```

Check status:

```bash
docker-compose -f deploy/docker-compose.yml ps
docker-compose -f deploy/docker-compose.yml logs -f hub-router
```

Stop:

```bash
docker-compose -f deploy/docker-compose.yml down
```

---

## Dockerfile Details

The image uses a two-stage build for a minimal attack surface:

```
Stage 1 — builder (golang:1.22-alpine)
  ├── Copy go.mod / go.sum → cache dependencies
  ├── Copy source
  └── Build fully static binary
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64
      -ldflags="-s -w -extldflags '-static'"
      → strips debug symbols, ~5 MB binary

Stage 2 — runtime (gcr.io/distroless/static-debian12:nonroot)
  ├── ~2 MB base image
  ├── No shell, no package manager, no OS tools
  ├── Copy /hub-router binary
  ├── Copy /etc/ssl/certs (for TLS to upstream services)
  ├── Copy /usr/share/zoneinfo (for timezone-aware logging)
  ├── EXPOSE 8080
  ├── HEALTHCHECK CMD ["/hub-router", "-healthcheck"]
  └── USER nonroot:nonroot (uid 65532)
```

The binary includes a built-in `-healthcheck` flag for Docker's `HEALTHCHECK` command — no `wget` or `curl` needed in the distroless image.

---

## docker-compose.yml Security Features

```yaml
security_opt:
  - no-new-privileges:true   # prevent setuid escalation

read_only: true               # immutable container filesystem
tmpfs:
  - /tmp:size=16m,mode=1777  # writable temp space only

user: nonroot                 # uid 65532

deploy:
  resources:
    limits:
      cpus: "1.0"
      memory: 256M
    reservations:
      cpus: "0.25"
      memory: 64M
```

---

## Health Check

hub-router exposes a `/health` endpoint that returns `200 OK` when healthy:

```bash
curl http://localhost:8080/health
```

```json
{"status": "ok", "queue_depth": 0, "uptime_seconds": 3600}
```

Docker health check configuration (from `docker-compose.yml`):

```yaml
healthcheck:
  test: ["/hub-router", "-healthcheck"]
  interval: 15s
  timeout: 5s
  start_period: 5s
  retries: 3
```

The `-healthcheck` flag makes the binary call `GET /health` on itself and exit 0 or 1 accordingly — no extra tools required.

---

## Kubernetes

Example deployment manifest:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hub-router
spec:
  replicas: 1
  selector:
    matchLabels:
      app: hub-router
  template:
    metadata:
      labels:
        app: hub-router
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        readOnlyRootFilesystem: true
      containers:
        - name: hub-router
          image: hub-router:latest
          ports:
            - containerPort: 8080
          env:
            - name: HR_ONLINE_API_KEYS
              valueFrom:
                secretKeyRef:
                  name: hub-router-secrets
                  key: online-api-keys
            - name: HR_LOCAL_API_KEYS
              valueFrom:
                secretKeyRef:
                  name: hub-router-secrets
                  key: local-api-keys
          resources:
            requests:
              cpu: "250m"
              memory: "64Mi"
            limits:
              cpu: "1"
              memory: "256Mi"
          livenessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 15
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 2
            periodSeconds: 10
          volumeMounts:
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: tmp
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: hub-router
spec:
  selector:
    app: hub-router
  ports:
    - port: 8080
      targetPort: 8080
```

Create the secrets:

```bash
kubectl create secret generic hub-router-secrets \
  --from-literal=online-api-keys=key1,key2 \
  --from-literal=local-api-keys=local-key1
```

---

## Prometheus Monitoring

If `HR_ADMIN_API_KEY` is not set, `/metrics` is unauthenticated. Prometheus scrape config:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: hub-router
    static_configs:
      - targets: ["hub-router:8080"]
    metrics_path: /metrics
    # If admin key is set:
    # authorization:
    #   type: Bearer
    #   credentials_file: /etc/prometheus/hub-router-token
```

---

## Graceful Shutdown

When the container receives `SIGTERM` (Docker stop, Kubernetes pod termination):

1. hub-router stops accepting new connections.
2. In-flight requests are given `HR_SHUTDOWN_TIMEOUT` (default 15s) to complete.
3. The process exits cleanly.

Tune `HR_SHUTDOWN_TIMEOUT` to match the typical processing time of your local server.

---

## Production Checklist

- [ ] Set `HR_ONLINE_API_KEYS` and `HR_LOCAL_API_KEYS` to strong random secrets (32+ chars)
- [ ] Set `HR_ADMIN_API_KEY` to protect `/metrics`
- [ ] Configure TLS termination upstream (nginx, Caddy, AWS ALB, etc.) — hub-router speaks plain HTTP
- [ ] Set `HR_RATE_LIMIT_RPS` and `HR_RATE_LIMIT_BURST` appropriate to expected traffic
- [ ] Set `HR_MAX_BODY_BYTES` to the maximum payload your use case requires
- [ ] Set `HR_QUEUE_MAX_SIZE` based on available memory (each queued request is ~1–2 KB + payload size)
- [ ] Configure resource limits in Docker/Kubernetes to prevent memory exhaustion
- [ ] Set `HR_LOG_LEVEL=info` and `HR_LOG_FORMAT=json` for production log aggregation
- [ ] Monitor `queue_depth` from `/health` — sustained high depth indicates local server backlog
- [ ] Set `HR_REQUEST_TTL` and `HR_RESULT_TTL` to match SLA expectations
- [ ] Ensure `HR_WRITE_TIMEOUT > HR_LONGPOLL_TIMEOUT` (default: 35s > 30s)

---

## Local Development

```bash
# Quick start (no Docker)
HR_ONLINE_API_KEYS=dev-online HR_LOCAL_API_KEYS=dev-local \
HR_LOG_LEVEL=debug HR_LOG_FORMAT=text HR_RATE_LIMIT_RPS=0 \
go run ./cmd/hub-router

# or using make:
make run
```

```bash
# Test connectivity
curl http://localhost:8080/health

# Submit a request
curl -X POST http://localhost:8080/request \
  -H "X-Online-API-Key: dev-online" \
  -H "Content-Type: application/json" \
  -d '{"payload": "hello"}'
```
