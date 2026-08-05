# 部署验证报告 (V5 Audit)

- 日期: 2026-08-06 04:47
- 环境: Windows 11 本地开发机 (无 docker / kubectl / helm / 云服务器 SSH)
- 项目: shark-socket (网关) + shark-mqtt (MQTT 代理)

## 1. Linux 编译验证

在本地对两个服务端程序做 Linux 目标交叉编译, 验证云服务器 (amd64) 可编译:

| 项目 | 命令 | 结果 | 产物 |
| --- | --- | --- | --- |
| shark-socket | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/shark-socket` | PASS | ELF 64-bit, statically linked |
| shark-mqtt | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/` | PASS | ELF 64-bit, statically linked |

`CGO_ENABLED=0` 静态链接, 产物可直接拷入最小化 Linux 容器/服务器运行, 无 glibc 依赖。

## 2. Docker 验证 (本地无 docker, 静态审查)

### shark-socket `deploy/docker/Dockerfile`
- 多阶段构建 (`golang:1.26-alpine` -> `alpine:3.22`)
- `CGO_ENABLED=0` 静态编译
- 非 root 运行 (`adduser -u 1000`)
- `HEALTHCHECK` 探测 `:18081/healthz`
- 暴露端口 18000/18080/18081

### shark-mqtt `deploy/docker/Dockerfile`
- 多阶段构建 (`golang:1.26-alpine` -> `alpine:3.21`)
- 非 root (`shark` 用户)
- `HEALTHCHECK` 探测 `:18999/healthz`
- 暴露端口 18983/18993/18999
- `docker-compose.yml` 含 prometheus 与 redis 后端示例

**注**: 本机无 docker 守护进程, `docker build` 未实际执行。上述内容在 CI 的
`docker-build`/`docker` job 中构建验证 (shark-socket `docker-build`, shark-mqtt
`docker` job 含启动 + healthz + MQTT smoke test)。

## 3. Kubernetes 验证

### 纯清单 (kustomize) 校验
对 shark-socket `deploy/k8s/` 与 shark-mqtt `deploy/k8s/app/` 全部 YAML 做语法解析:

- shark-socket: namespace, serviceaccount, configmap, networkpolicy, pdb, hpa,
  kustomization, deployment, service —— 全部 `OK`
- shark-mqtt: namespace, configmap, deployment, service, hpa, networkpolicy,
  ingress, kustomization —— 全部 `OK`

### Helm chart 校验
shark-socket `deploy/helm/shark-socket/` 与 shark-mqtt `deploy/k8s/helm/shark-mqtt/`
为标准 Go 模板结构 (`{{ .Values... }}` 占位), 需经 `helm template` 渲染后才是合法
YAML; 本机无 helm, 已通过结构审查确认模板/values 齐全 (_helpers.tpl, values.yaml,
values-prod.yaml, deployment/service/configmap/hpa/ingress/networkpolicy/servicemonitor)。

## 4. 云服务器部署流程 (待执行清单)

本环境无云服务器访问凭据。以下为在目标云服务器上应执行的完整流程:

### shark-socket
```bash
# 1. 上传产物
scp /tmp/shark-socket-linux root@<HOST>:/usr/local/bin/shark-socket

# 2. 编写配置 (可选)
cat > /etc/shark-socket/config.json <<'EOF'
{ "health_addr": "0.0.0.0:18081", "metrics_addr": "0.0.0.0:18080",
  "protocols": [ {"name":"tcp","addr":"0.0.0.0:18000"},
                 {"name":"coap","addr":"0.0.0.0:5683","mode":"lwm2m"} ] }
EOF

# 3. 运行并验证
shark-socket -config /etc/shark-socket/config.json &
curl -sf http://127.0.0.1:18081/healthz && echo ok
curl -sf http://127.0.0.1:18081/readyz && echo ready
curl -sf http://127.0.0.1:18080/metrics | head
```

### shark-mqtt
```bash
scp /tmp/shark-mqtt-linux root@<HOST>:/usr/local/bin/shark-mqtt
shark-mqtt -addr=:18983 -allow-all   # 生产环境应配置认证
# 验证
mosquitto_sub -h <HOST> -p 18983 -t test -C 1 &
mosquitto_pub -h <HOST> -p 18983 -t test -m hello
curl -sf http://127.0.0.1:18999/healthz
```

### Docker / K8s
```bash
docker build -f deploy/docker/Dockerfile -t shark-socket:latest .
kubectl apply -k deploy/k8s/
helm install shark-socket deploy/helm/shark-socket/
```

## 5. 结论

- 两个服务端程序均已通过 Linux 目标编译验证 (静态链接, 可直接部署)。
- Dockerfile 与 K8s 清单通过静态审查与 YAML 语法校验; 云服务器上的实际
  docker 构建 / k8s 部署 / 冒烟测试需在有 docker + kubectl + helm 的服务器上
  按第 4 节流程执行。
