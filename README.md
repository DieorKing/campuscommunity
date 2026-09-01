# CampusCommunity — 校园拼单社区

基于 Go + Vue3 的前后端分离校园拼单平台，覆盖**高并发抢单防超卖、实时热榜、订单超时自动关单、站内通知**等核心场景。Redis + RabbitMQ 异步削峰，Docker Compose 五容器一键部署，阶梯压测单机 **5800+ QPS（P99 < 30ms，零错误）**。

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.26 · Gin · GORM v2 · Viper · zap |
| 数据 | MySQL 5.7（utf8mb4）· Redis 7 · RabbitMQ 3.13 |
| 前端 | Vue3 · Element Plus · Pinia · axios · Vite |
| 部署 | Docker Compose（五容器）· Nginx 反代/静态托管 · go-wrk 压测 |

## 架构

```
                       ┌────────────────────────────────────────────┐
                       │                  宿主机 :80                 │
                       ▼                                            │
                ┌─────────────┐   /api/  /uploads/                   │
                │    Nginx    │ ────────────────────────┐            │
                │ 静态托管+Gzip│  (运行时 resolver 解析)    │           │
                │  + 反向代理  │                          ▼            │
                └─────────────┘                 ┌─────────────────┐   │
                       │                        │  Backend (Go)   │   │
                       │ 静态资源                │  Gin + 业务逻辑  │   │
                       ▼                        └─┬─────┬─────┬───┘   │
              ┌──────────────┐                    │     │     │       │
              │ frontend/dist│                    ▼     ▼     ▼       │
              └──────────────┘          ┌────────┐ ┌───────┐ ┌──────────┐
                                        │ MySQL  │ │ Redis │ │ RabbitMQ │
                                        │  5.7   │ │   7   │ │  3.13    │
                                        └────────┘ └───────┘ └──────────┘
      宿主机调试端口: MySQL 3307 · Redis 6379 · RabbitMQ 5672/15672(UI) · Backend 8080
```

- **健康检查链**：MySQL / Redis / RabbitMQ 全部 healthy 后才启动 backend（`depends_on: service_healthy`），杜绝连库竞态
- **数据卷**：mysql-data / redis-data / rabbitmq-data / avatar-uploads 四个命名卷，容器重建数据不丢
- **配置外置**：backend 配置文件挂载进容器而非打进镜像（12-factor），改配置不重建镜像

## 核心特性

### 高并发抢单与异步削峰
- **分布式锁 + Lua 原子预扣**：同拼单竞争用 Redis 分布式锁（SET NX EX + Lua 释放）串行化；Lua 脚本内原子完成「成员判重 → 库存扣减 → 成员登记」，杜绝超卖与重复参与
- **MQ 异步建单**：抢单接口只做预扣 + 消息投递（毫秒级响应「受理中」），订单由消费者异步创建，`(user_id, good_id)` 唯一索引保证幂等消费
- **快速失败**：竞争失败 / 库存不足 / 重复参与在锁与 Lua 阶段直接拒绝，不进入下游

### 实时热榜
- Redis ZSet 计分板：发布入榜、建单事件 ZINCRBY 累加、查询 ZREVRANGE + MySQL 回表过滤终态
- 分数累加失败走补偿任务重算：以订单 COUNT 绝对值覆盖 ZSet 分数，规避「相对操作不可重放」的幂等陷阱

### 轻量延时任务引擎
- ZSet（score = 到期时间戳）+ Goroutine 周期扫描 + ZREM 原子弹出，驱动**订单超时自动关单**与**拼单截止成团判定**，替代低效扫表
- 到期任务的 ZREM 抢占语义天然支持多实例扩展

### 消息可靠性三层防线
三类故障各接一层，互补不冗余：

| 故障类型 | 防线 |
|---|---|
| 消费报错 | MQ 手动 ack + 指数退避重试 |
| 主流程成功后的旁路失败（热榜累加/关单/通知落库） | 补偿任务表 + ZSet 延时调度，退避重试超限转终态留人工 |
| 消息静默丢失 | 两阶段标记（pending → success）+ 滞留对账重发 |

### 双层限流
- **注册接口**：IP 维度令牌桶（rate 0.1/s + burst 3）防脚本轰炸，保护 bcrypt 计算资源
- **抢单/轮询接口**：用户维度令牌桶（rate 5/s + burst 10），参照合法用户行为上限设定，单用户无法占满系统容量
- 同一 `Limiter` 接口两种 key 实现，多实例演进时以 Redis+Lua 替换内存桶，挂载代码零改动

### 其他
- **头像上传**：multipart 校验（大小上限 + 文件头魔数嗅探，不信任客户端声明的格式），user_id 命名落盘防路径遍历，Nginx `/uploads/` 反代
- **可观测**：zap 结构化日志（仅错误与慢请求）、pprof 性能采样端点（debug 模式）
- **安全基线**：JWT 鉴权（7 天过期）、bcrypt 密码哈希、资源所有权校验、雪花 ID 字符串化防 JS 精度丢失

## 快速开始

### 前置要求

- Docker & Docker Compose v2
- Node.js ≥ 18（仅构建前端产物用）

### 三步部署

```bash
# 1. 构建前端（产物 dist/ 挂载进 Nginx 托管）
cd frontend
npm install
npm run build
cd ..

# 2. 生成后端容器配置（example 密码可直接跑通，生产请替换）
cp backend/internal/conf/config.container.example.yaml backend/internal/conf/config.container.yaml

# 3. 一键启动（首次自动构建 backend 镜像，需拉取 golang/alpine 基础镜像）
docker compose up -d --build
```

启动完成后访问 **http://localhost**，RabbitMQ 管理台 **http://localhost:15672**（trKing / 12345678a）。

> 若宿主机 80 / 8080 / 3307 / 6379 / 5672 端口被占用，请先释放或修改 `docker-compose.yml` 的端口映射。

### 本地开发模式（不用容器跑后端）

```bash
# 后端：复制示例配置并按需修改，直连容器栈的宿主映射端口
cp backend/internal/conf/config.example.yaml backend/internal/conf/config.yaml
cd backend && go run ./cmd -f internal/conf/config.yaml

# 前端：Vite 代理 /api 与 /uploads 到后端
cd frontend && npm run dev
```

## API 概览

统一前缀 `/api/v1`，响应格式 `{"code": 0, "msg": "success", "data": {...}}`（`code=0` 为成功）。除注册/登录/拼单列表外均需 `Authorization: Bearer <token>`。

| 模块 | 方法 | 路径 | 说明 |
|---|---|---|---|
| 认证 | POST | /auth/register | 注册（IP 限流内） |
| 认证 | POST | /auth/login | 登录，返回 JWT |
| 用户 | GET / PATCH | /user/profile | 查看 | 部分更新资料 |
| 用户 | PUT | /user/address | 修改收货地址 |
| 用户 | POST | /user/avatar | 头像上传（multipart，≤5MB） |
| 拼单 | POST | /group-buy | 发布拼单 |
| 拼单 | GET | /group-buy/list | 列表（latest / hot 热榜排序，分页） |
| 拼单 | GET | /group-buy/:id | 详情（含参与名单） |
| 拼单 | POST | /group-buy/:id/grab | 抢单（异步受理，用户限流内） |
| 拼单 | GET | /group-buy/:id/status | 抢单结果轮询 |
| 订单 | POST | /order/:id/pay | 支付（模拟，状态机守卫） |
| 订单 | POST | /order/:id/cancel | 取消（仅待支付可取消，名额退池） |
| 订单 | GET | /order/list | 我的订单（状态筛选 + 分页） |
| 通知 | GET | /notification/list | 通知列表 + 未读数（一次返回） |
| 通知 | POST | /notification/:id/read | 标记已读（幂等） |

## 项目结构

```
├── backend/
│   ├── cmd/main.go              # 启动入口：装配配置/日志/DB/Redis/MQ/路由
│   ├── internal/
│   │   ├── controller/          # HTTP 层：参数绑定 → 调 logic → 统一响应
│   │   ├── logic/               # 业务层：抢单编排/订单状态机/热榜/补偿/对账
│   │   ├── dao/
│   │   │   ├── mysql/           # GORM 数据访问
│   │   │   └── redis/           # 库存/Lua 预扣/分布式锁/热榜/延时队列
│   │   ├── middleware/          # JWT 鉴权 / 令牌桶限流
│   │   ├── mq/                  # RabbitMQ 注册表模式：队列-路由键-消费者
│   │   ├── model/               # 数据模型（雪花 ID，JSON 序列化为字符串）
│   │   ├── conf/                # Viper 配置加载 + 各环境示例配置
│   │   └── router/              # 路由注册与中间件挂载
│   └── Dockerfile               # 多阶段构建（golang 编译 → alpine 运行）
├── frontend/                    # Vue3 + Element Plus + Pinia
├── nginx/nginx.conf             # 静态托管 + /api /uploads 反代 + Gzip
├── docker-compose.yml           # 五容器编排（healthcheck 依赖链）
└── README.md
```

## 配置说明

所有敏感项在 example 配置中统一为占位值 `12345678a`，可直跑通全链路；生产部署请全部替换：

| 配置项 | 位置 | 说明 |
|---|---|---|
| MySQL 密码 | compose `MYSQL_ROOT_PASSWORD` + 配置 `mysql.password` | 两处需一致 |
| Redis 密码 | compose `--requirepass` + 配置 `redis.password`（healthcheck 内含） | 两处需一致 |
| RabbitMQ 账密 | compose `RABBITMQ_DEFAULT_*` + 配置 `rabbitmq.*` | 两处需一致 |
| JWT secret | 配置 `jwt.secret` | 换任意随机串 |
| 上传目录 | 配置 `upload.dir` | 容器内对应命名卷挂载点 `/app/uploads` |

> **换密码须知**：MySQL / RabbitMQ 的密码在数据卷首次初始化时烙定，仅改配置不生效——需 `docker compose down -v` 删除卷后重建（会清空全部数据）。

## 压测参考

go-wrk 阶梯加压（容器栈直连 backend 8080，热态预热后取值）：

| 接口 | 并发 100 QPS | 说明 |
|---|---|---|
| /test（纯框架基线） | 6299 | 路由 + JSON 往返上限 |
| /group-buy/list | 3582 | 含 COUNT + 分页两条 SQL |
| /group-buy/:id/grab | 5397 | 锁 + Lua 预扣 + MQ 投递全链路 |

全接口零错误、零库存回退；饱和点判定与瓶颈归因（CPU 核算 + pprof 火焰图）显示系统为 I/O 密集型，限流过载场景被拒请求 P99 < 5ms。

## 部署排障

| 现象 | 原因与处理 |
|---|---|
| backend 反复重启 | 查 `docker compose logs backend`；多数为配置与容器环境不匹配（密码/地址），确认使用的是 `config.container.yaml` 且与 compose 密码一致 |
| Redis 冷启动后热榜/库存为空 | 预期行为——Redis 层是可重建的加速层，发布新拼单即恢复；热榜分数由补偿重算机制自愈 |
| 构建镜像时拉取 golang 基础镜像缓慢 | 需配置 Docker 镜像加速或代理；构建分层已做依赖缓存（go.mod 未变时跳过下载） |
| 页面 502 | backend 容器未就绪或刚重建换 IP——Nginx 已配置运行时 resolver，等待数秒自动恢复 |
| 上传头像 404 | 确认 `config.container.yaml` 的 `upload.dir=/app/uploads` 与 compose 的 avatar-uploads 卷挂载一致 |

## License

MIT
