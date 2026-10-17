# K8s / Helm 部署链路验证报告

- 日期: 2026-10-16 00:17 (+0800)
- 主机: 云3 `8.130.190.141`(Ubuntu 26.04.1, 8 vCPU / 16 GB, 公网 100 Mbps 峰值), 单节点 k3s
- 集群: k3s `v1.36.5+k3s1`, containerd `2.3.4-k3s1.36`, Helm `v3.21.0`
- 源码: HEAD `0d07f19f7` + 本次发现的端口名修复(未提交)
- 命名空间: `mqtt`, release `shark-mqtt`, 最终 revision 5
- 报告范围: **真实集群上的部署链路**, 不含性能压测(见 `docs/benchmarks/2026-10-15-cross-host-throughput.md`)

用户约束: 云1(2C2G)不参与本轮验证, K8s / Helm 链路整体在云3 上完成.

---

## 1. 结论

| 结论 | 证据 |
| --- | --- |
| 多端口 Helm 部署**此前是坏的**, 现已修复 | 修复前 `helm upgrade` 被 API 直接拒绝(见 Section 3) |
| 修复后 Service / Deployment / Endpoints 三者端口名一致 | Section 4 |
| 每个声明的监听端口**真的可达**, 未声明端口真的不可达 | Section 5(含对照端口) |
| 经 ClusterIP 的 QoS 0 / QoS 1 往返完整 | Section 5 |
| 滚动更新正常, 无 `FailedPreStopHook`, 无探针失败 | Section 6 |
| 管理/健康端点在集群内可用 | Section 7 |

一句话: 本次验证发现并修复了一个**只在多端口配置下才会触发**的真实缺陷, 修复后整条链路全绿;
单端口默认配置从未触发该缺陷, 因此既有的 `helm lint` / `helm template` / CI 全部漏过.

---

## 2. 环境障碍与绕过

云3 上 **Docker Hub 完全不可达**: 镜像站的 token 端点仍指向 `auth.docker.io`, 超时;
`alpine:3.21`(4.7 MB)也拉不动, 进度条停在 fs layer. 因此整条链路改为**零 registry 访问**:

| 环节 | 常规做法 | 本机替代方案 |
| --- | --- | --- |
| broker 镜像 | `docker build`(多阶段, 需 `golang` 基础镜像) | 宿主机 `CGO_ENABLED=0 go build -ldflags="-s -w"`, `Dockerfile` 为 `FROM scratch` + `ca-certificates.crt` + 静态二进制; `docker save` -> `k3s ctr images import` |
| pause(infra)镜像 | 拉取 `rancher/mirrored-pause:3.10.2` | Go 写 sleep 型静态二进制, 打同名 tag 后 `k3s ctr images import` |

两点值得记住:

- **containerd 不继承 docker 的 registry-mirror**. 写 `/etc/rancher/k3s/registries.yaml` 会落盘成
  `/var/lib/rancher/k3s/agent/etc/containerd/certs.d/docker.io/hosts.toml`, 但**仍然失败**;
  在 registry 不可达的机器上, 唯一可靠的办法是把镜像**直接灌进 containerd**.
- 导入后的证据: `k3s ctr images ls` 中 `docker.io/library/shark-mqtt:test` 4.6 MiB、
  `docker.io/rancher/mirrored-pause:3.10.2` 668 KiB, 均为 `linux/amd64`.

失败现场(导入 pause 之前), 这四条事件来自**已废弃的旧 ReplicaSet**, 不是本次验证的 Pod:

```
Warning  FailedCreatePodSandBox  pod/shark-mqtt-6994475555-xrb9k
  failed to pull image "rancher/mirrored-pause:3.10.2":
  Head "https://registry-1.docker.io/v2/rancher/mirrored-pause/manifests/3.10.2":
  dial tcp 157.240.2.50:443: i/o timeout
```

---

## 3. 缺陷: 多端口端口名超过 IANA_SVC_NAME 上限

### 3.1 现场

以 `config.listenAddrs = [":18983", ":28983"]` 安装时, `helm upgrade` 被 API server 直接拒绝:

```
Error: UPGRADE FAILED: cannot patch "shark-mqtt" with kind Service: Service "shark-mqtt" is invalid:
spec.ports[1].targetPort: Invalid value: "mqtt-extra-28983": must be no more than 15 characters
&& cannot patch "shark-mqtt" with kind Deployment: Deployment.apps "shark-mqtt" is invalid:
spec.template.spec.containers[0].ports[1].name: Invalid value: "mqtt-extra-28983": must be no more than 15 characters
```

### 3.2 根因

Service port name 与 container port name 都是 `IANA_SVC_NAME`, **上限 15 字符**.
旧模板从监听地址派生名字:

```yaml
- name: mqtt-extra-{{ . | replace ":" "-" | replace "." "-" | trunc 12 | trimSuffix "-" | trimPrefix "-" }}
```

`mqtt-extra-` 前缀占 11 字符, 只剩 4 个字符可用. 而模板里的 `trimPrefix "-"` 恰好把
`:28983` 变成 `-28983` 再剥成 `28983`, 剩 5 位 -> `mqtt-extra-28983` = **16 字符**, 越界.
换成 `0.0.0.0:28983` 更糟: `0-0-0-0-28983`(13 位)被 `trunc 12` 截成 `0-0-0-0-2898`,
拼出 23 字符. 也就是说**这条 `range` 分支渲染出的名字恒越界**, 只要 `listenAddrs` 多于一项就必炸.

### 3.3 为什么既有检查没发现

| 检查 | 能否发现 | 原因 |
| --- | --- | --- |
| `helm lint` | 否 | 只查语法与约定, 不做 API 校验 |
| `helm template` | 否 | 只渲染 YAML, 不提交 API |
| CI | 否 | 不跑真实集群 |
| `deploy_test.go`(44 个用例) | 否 | 解析渲染后的 YAML 文本, 不施加 15 字符约束 |

**只有真实集群能发现** -- 这正好复现了项目记忆里那条经验: "`helm lint` 只保证语法,
`helm template` 看渲染值; 只有实部署 + 真连一次能发现真实问题".

### 3.4 修复

名字只从**端口号**派生, 不再从监听地址派生:

```yaml
- name: mqtt-{{ . | replace ":" "" | trimPrefix "0.0.0.0" | trimPrefix ":" | int }}
  protocol: TCP
  port: {{ . | replace ":" "" | trimPrefix "0.0.0.0" | trimPrefix ":" | int }}
  targetPort: mqtt-{{ . | replace ":" "" | trimPrefix "0.0.0.0" | trimPrefix ":" | int }}
```

`mqtt-` 占 5 字符, 端口最长 5 位 -> 最长 10 字符, 留 5 字符余量; 且端口号天然唯一,
不需要 `trunc` 那种会咬人的截断.

改动文件:

- `deploy/k8s/helm/shark-mqtt/templates/service.yaml`(2 处: `name` 与 `targetPort`)
- `deploy/k8s/helm/shark-mqtt/templates/deployment.yaml`(1 处: `name`)

云3 上的 `/root/shark-mqtt` 已同步为同一份内容, sha1 逐字节一致:

```
service.yaml     e506ce1535c39cd9269d7c5b618ae80f80b3e75c
deployment.yaml  abc6f3db3d51296114db1effbbb44d196a730519
```

---

## 4. 渲染结果

`helm lint`: `1 chart(s) linted, 0 chart(s) failed`.

Service ports(`kubectl get svc -o jsonpath='{.spec.ports}'`):

```json
[{"name":"mqtt","port":18983,"protocol":"TCP","targetPort":"mqtt"},
 {"name":"mqtt-28983","port":28983,"protocol":"TCP","targetPort":"mqtt-28983"},
 {"name":"metrics","port":18999,"protocol":"TCP","targetPort":"metrics"}]
```

Container ports:

```json
[{"containerPort":18983,"name":"mqtt","protocol":"TCP"},
 {"containerPort":28983,"name":"mqtt-28983","protocol":"TCP"},
 {"containerPort":18999,"name":"metrics","protocol":"TCP"}]
```

Endpoints 三个端口全部解析到同一个 Pod IP:

```json
{"addresses":[{"ip":"10.42.0.148","nodeName":"iz0jlcgedkf3ybet8e1quuz",
               "targetRef":{"kind":"Pod","name":"shark-mqtt-66f97585d-smp6f"}}],
 "ports":[{"name":"mqtt-28983","port":28983},{"name":"metrics","port":18999},{"name":"mqtt","port":18983}]}
```

端口名长度复核(上限 15): `mqtt`=4, `mqtt-28983`=10, `metrics`=7.

---

## 5. 连通性(真实 MQTT 往返)

`loadgen` 从节点上直连, `-subscribers 1 -publishers 2 -rate 100 -duration 5s -payload 32`.

| 目标 | QoS | sent | received | delivery_ratio | P50 |
| --- | --- | --- | --- | --- | --- |
| PodIP:18983 | 0 | 500 | 500 | 1.0000 | 190 us |
| PodIP:28983 | 0 | 500 | 500 | 1.0000 | 187 us |
| PodIP:38983(**对照, 未配置**) | 0 | -- | -- | 无输出(连接被拒) | -- |
| ClusterIP:18983 | 1 | 500 | 500 | 1.0000 | 286 us |

三点说明:

- **对照端口必须保留**. 早先一版用 `grep -E '"(sent|received)"'` 判成功, 未配置的 38983
  因为 grep 空输出被**误判为成功**. 改成 `/dev/tcp` 原始探测 + 对照端口后才得到可信结论.
- 两轮结果一致(修复后首次验证与模板同步后的 revision 5 复跑), 18983/28983 均 500/500.
- `/root/loadgen` 报告 `broker_cpu_cores: 0.014` -- 这是 loadgen 读 `/proc/PID/stat` 得到的值,
  但它读的是**节点上**的 PID, 容器内 broker 的 CPU 需要从 cgroup 取; 此处的核数不具参考性.

---

## 6. 滚动更新与 preStop

```
deployment "shark-mqtt" successfully rolled out
before=shark-mqtt-684c4f5d7d-m5twf  after=shark-mqtt-66f97585d-smp6f
```

当前 Pod 事件**只有四条 Normal**, 无 `FailedPreStopHook`、无探针失败、无 `BackOff`:

```
Normal  Scheduled   Successfully assigned mqtt/shark-mqtt-66f97585d-smp6f to iz0jlcgedkf3ybet8e1quuz
Normal  Pulled      Container image "shark-mqtt:test" already present on machine
Normal  Created     Container created
Normal  Started     Container started
```

这说明 chart 里用 **K8s 原生 `sleep` handler**(而非 `exec`)的设计是对的: 原生 handler 由
kubelet 执行, 与镜像内容无关; 而本镜像是 `FROM scratch`, 任何 `exec` 型 preStop 都会静默失败
(`FailedPreStopHook` 只是 Warning, 滚动更新照样"成功", 极易漏掉).

滚动更新后的 MQTT 复核: `delivery_ratio=1.0011, latency_p50_us=154, received=901, sent=900`.

---

## 7. HTTP 端点(经 ClusterIP)

| 端点 | 结果 |
| --- | --- |
| `http://<ClusterIP>:18999/metrics` | 34 行 `shark_mqtt_` 指标 |
| `http://<ClusterIP>:18999/healthz` | 200 |
| `http://<ClusterIP>:18999/readyz` | 200 |

---

## 8. 本轮未覆盖

| 项 | 说明 |
| --- | --- |
| NodePort / LoadBalancer | 只验了 `ClusterIP`; `service.type` 另两态未实跑 |
| 多副本 + PDB | 单副本; 多副本下的会话亲和与滚动并发未验 |
| PVC / 持久化 | 用了默认 emptyDir 类配置, 未验 `local-path-provisioner`(该组件在本机因镜像不可达未就绪) |
| TLS / WSS 监听器 | 未配置证书 |
| Helm hook / Job | chart 未使用 |
| 真实 registry 推送 | 本机 registry 不可达, 镜像走 `ctr images import` |

---

## 9. 复现命令

```bash
# 前提: 镜像已导入 containerd
k3s ctr images import /tmp/shark-mqtt-test.tar

helm lint deploy/k8s/helm/shark-mqtt

# 关键: 必须传两个以上 listenAddrs, 单端口不会触发被修复的缺陷
helm upgrade --install shark-mqtt deploy/k8s/helm/shark-mqtt \
  --namespace mqtt --create-namespace \
  --set config.listenAddrs[0]=':18983' \
  --set config.listenAddrs[1]=':28983' \
  --set service.type=ClusterIP \
  --set image.repository=shark-mqtt --set image.tag=test --set image.pullPolicy=IfNotPresent \
  --wait --timeout 180s

kubectl -n mqtt get svc,ep,deploy,po
kubectl -n mqtt describe po -l app.kubernetes.io/name=shark-mqtt | sed -n '/^Events:/,$p'

# 连通性(务必带一个未配置端口做对照)
PODIP=$(kubectl -n mqtt get po -l app.kubernetes.io/name=shark-mqtt -o jsonpath='{.items[0].status.podIP}')
go run ./tools/loadgen -addr "$PODIP:18983" -subscribers 1 -publishers 2 -qos 0 \
  -rate 100 -duration 5s -payload 32 -json
go run ./tools/loadgen -addr "$PODIP:38983" -subscribers 1 -publishers 1 -qos 0 \
  -rate 10 -duration 3s -payload 32          # 期望: 连接被拒

kubectl -n mqtt rollout restart deploy/shark-mqtt
kubectl -n mqtt rollout status deploy/shark-mqtt --timeout=180s
```

---

## 10. 待办

1. 提交 `service.yaml` / `deployment.yaml` 的端口名修复(本地工作树与云3 已一致, 尚未 commit).
2. 给 `tests/integration/deploy_test.go` 补一条**端口名长度 <= 15** 的断言, 让这类缺陷在
   CI 里就能被抓住, 而不必依赖真实集群.
3. 补一个多端口的 `helm template` 渲染快照, 覆盖 `listenAddrs` 长度 >= 2 的分支.
