# 设计规格：存活判定与排序模型

本文件记录磁力聚合程序的核心判定与排序设计（由架构评审产出，代码实现以此为准）。
用户痛点原文：**「冷门资源容易搜到，优先活跃磁力（可过滤死链）」**，且**「要搜索的资源包含海外的」**。

---

## 0. 环境实测约束（决定了模型必须能降级）

本机（macOS arm64，Clash TUN fake-IP 模式，HTTP/SOCKS 代理在 127.0.0.1:7890）实测：

| 通道 | 结果 |
|---|---|
| 出站 UDP（tracker scrape、DHT） | **全部超时**，被 TUN 代理阻断 |
| HTTP/HTTPS tracker | 除 `tracker.empire-js.us` 外**全部不可达**（返回空响应） |
| 境外站点（nyaa.si、btdig.com、solidtorrents.to、magnetdl.com、1337x.to、yts.mx、anidex.moe、rutracker.org、thepiratebay.org） | **多数被 GFW 阻断**；`bt4gprx.com`、`nyaa.ink`、`btsow.com` 可达 |
| 境外链路稳定性 | 同一 URL 连续请求成功率约 **50%**（`nyaa.ink` 实测 4/8） |
| 可稳定直连的引擎 | `apibay.org`（TPB 官方 JSON API）、`bitsearch.eu`、`btsearch.love`、`tokyotosho.info`、`rutor.info` |

**结论**：网络层探测在多数用户网络上不可用，模型必须**以元数据为主、网络探测为增强**，
并且必须严格区分「测到是 0」与「根本没测到」。

---

## 1. 数据契约

```go
type Result struct {
    Title, InfoHash, MagnetURI string
    SizeBytes                  int64
    Seeders, Leechers          int   // -1 = 引擎未提供（model.Unknown）
    PublishDate                time.Time
    ListedAt                   time.Time
    Source, URL, Category      string
    Rank                       int   // 该结果在引擎返回列表中的位置
}

type Liveness struct {
    InfoHash                                  string
    TrackerComplete, TrackerIncomplete        int  // -1 = 未知
    DHTPeers                                  int  // -1 = 未知
    Trackers                                  []string
    Errors                                    []string
    ProbedAt                                  time.Time
}
```

`Unknown = -1` 与真实 `0` 的区分是整个模型成立的前提。

---

## 2. 存活判定

### 2.1 证据优先级

`tracker 探测 > DHT 探测 > 引擎宣称`。探测值与引擎宣称冲突时，**状态以探测为准**，
但置信度打折（见 2.3）。引擎宣称仍计入多引擎佐证数 K。

### 2.2 状态判定表

设 S = 做种数（tracker complete / DHT peers / 引擎宣称的 seeders），
L = 下载者数（tracker incomplete / 引擎宣称的 leechers）：

| 条件 | 状态 | 说明 |
|---|---|---|
| S ≥ 3 | **Alive** | 活跃，下载顺畅 |
| 1 ≤ S ≤ 2 | **Cold** | 冷门但确实活着 |
| S = 0 且 L ≥ 1 | **Cold** | 无做种者、仅有下载者，随时可能彻底失败（置信度 ×0.9） |
| S = 0 且 L = 0，**且探测成功返回** | **Dead** | 死亡的**正面证据** |
| S = 0 且 L = 0，仅引擎宣称，且资源年龄 ≥ 7 天且 ≥ 2 个引擎一致报 0 | **Dead** | 弱但可接受的死亡证据（置信度 ×0.85） |
| S = 0 且 L = 0，仅引擎宣称，其余情况 | **Unknown** | 不判死 |
| 完全没有证据 | **Unknown** | 绝不因"没测到"而判死 |

### 2.3 置信度

```
lag        = now - observedAt
freshness  = 2^(-lag / H)          H_tracker=30m  H_dht=15m  H_eng=6h
durability = 0.55 + 0.45*exp(-ageDays/120)      资源年龄未知时取 0.75
conflict   = 0.8（探测与引擎宣称在"有无对等端"上矛盾时）
confidence = clamp(base * freshness * durability * conflict, 0, 0.95)
```

`base` 取值：

| 证据 | base |
|---|---|
| tracker 探测，1 个 tracker 响应 | 0.90 |
| tracker 探测，≥2 个 tracker 响应 | 0.95 |
| DHT 探测 | 0.60 |
| 引擎宣称，K=1 | 0.45 |
| 引擎宣称，K=2 | 0.65 |
| 引擎宣称，K=3 | 0.75 |
| 引擎宣称，K≥4 | 0.80 |

K = 独立佐证该哈希的引擎数量。多引擎一致比单一引擎的宣称可信得多，这是本网络环境下最重要的置信度来源。

### 2.4 为什么"非零宣称"不能单独判为 Alive

用户要求：**冷门且确有生机的资源（1–3 个做种）必须排在"宣称 500 做种但无法证实"的热门资源之前**。
若把宣称直接当事实，热门资源会碾压冷门资源。因此：宣称只贡献**置信度**与**佐证数**，
其**量级**在排序中会被饱和函数压缩（见 3.2），真正起决定作用的是置信度。

---

## 3. 排序

### 3.1 公式

```
Score = 0.55*relevance + 0.33*lifeAxis + 0.05*swarmSize + 0.07*freshness

lifeAxis  = 0.15 + 0.85 * conf^1.6 * sat(peers)      sat(p) = min(1, log2(1+p)/log2(5))
swarmSize = min(1, log2(1+peers)/log2(201)) * conf
freshness = exp(-ageDays/45)                          发布日期未知时取 0.30
```

- `sat` 在 4 个对等端处饱和：3 个做种与 500 个做种的 lifeAxis 几乎相同，
  避免"热门度"压倒一切。
- `conf^1.6` 是关键：置信度低的"宣称"会被显著压缩（0.45 → 0.28），
  于是**被证实的冷门资源（conf≈0.9）会压过未经证实的热门资源（conf≈0.45）**。
- `lifeAxis` 的下限 0.15 保证 Unknown 状态既不会被顶到最前，也不会被沉到底。

数值验证（同一相关度下）：

| 场景 | lifeAxis 贡献 | swarm 贡献 | 合计 |
|---|---|---|---|
| 冷门 3 做种，conf 0.65 | 0.172 | 0.009 | **0.181** |
| 热门 500 做种，conf 0.45 | 0.129 | 0.023 | 0.152 |
| 热门 500 做种，conf 0.65（多引擎佐证） | 0.192 | 0.033 | **0.225** |

即：冷门 + 佐证 > 热门 + 无佐证；热门 + 佐证 > 冷门 + 佐证。两条需求同时满足。

### 3.2 相关度

```
textRelevance = 0.55*coverage + 0.20*orderBonus + 0.15*phraseBonus + 0.10*precision
relevance     = 0.65*textRelevance + 0.35*enginePrior
enginePrior   = 1/(1+Rank)         Rank 未知时取 0.5
```

**硬门（不满足即淘汰）**：
- `coverage ≥ 0.6`
- 查询中的**锚点词元**（数字/版本号，如 `03`、`24.04`）必须全部命中
  —— 这是防止"第 4 集的低码率版本"挤掉"第 3 集的正确版本"的关键
- `textRelevance < 0.15 且 Rank > 10` → 淘汰

**enginePrior 的意义**：引擎已经用自己的索引（含日文/英文条目的中文别名）完成了匹配，
它在结果中的**位置**是独立于文本重叠的真实相关度信号。
中文查询检索日文/英文标题的海外资源时，文本相关度必然很低，靠 enginePrior 兜底。

### 3.3 聚类

```
clusterKey = resolutionTier + "|" + langKey + "|" + contentKey
```

- `resolutionTier`：uhd / 1080p / 720p / sd / any
- `langKey`：zh / en / ja / ko / ... / multi / any
- `contentKey`：去掉画质、语言、发布组、版本形态词元后的内容词元（排序去重后用 `.` 连接）

效果：**1080p 与 720p 是不同簇**（不同文件，确实是不同下载），
**`[GroupA]` 与 `[GroupB]` 的同一资源是同一簇**（发布组名不进入簇键）。
语言进入簇键，是为了让海外版本不被中文版本淹没。

同一簇在输出中最多保留 `--per-cluster`（默认 2）条。

---

## 4. 冷门友好机制

**采用：顶部配额（reserved quota）**。在 top-K 中为"稀有但确实活着"的资源预留席位。

- 稀有定义：可信对等端数 ≤ `--rare-max-peers`（默认 4）且状态为 Cold
- 门槛：相关度 ≥ 0.55 且置信度 ≥ 0.40（防止把死链抬进顶部）
- 配额默认 `ceil(0.2 * limit)`，`--rare-quota=0` 可关闭

**被否决的方案**：

| 方案 | 否决理由 |
|---|---|
| (a) 结果集内归一化 | 只能在集合内排序，抬不高"稀有项"越过热门墙；且分数随查询漂移，不可测试 |
| (c) MMR 多样性重排 | λ 不可解释；会惩罚合法的多画质版本（1080p BluRay + 1080p WEBRip） |
| (d) 热门/稀有分区 | 改变用户心智模型（"我的资源为什么不是第一"），不作为默认 |
| (e) 显式模式切换 | 与"冷门资源应当**容易**被找到"相悖，用户不该需要先知道要切模式 |

---

## 5. 死链过滤 UX

| 标志 | 取值 | 默认 | 含义 |
|---|---|---|---|
| `--dead-filter` | `off` / `hide` / `mark` / `strict` | `hide` | hide=隐藏被判死的；strict=额外要求有正面死亡证据 |
| `--min-peers` | int | 0 | 低于该可信对等端数的结果被丢弃 |
| `--min-confidence` | float | 0.0 | 低于该置信度的结果被丢弃 |
| `--rare-quota` | int | auto | 顶部稀有席位数量 |
| `--rare-max-peers` | int | 4 | 稀有的对等端上界 |
| `--per-cluster` | int | 2 | 每个簇最多输出条数 |
| `--cluster-key` | `resolution` / `full` / `none` | resolution | 聚类粒度 |

默认 `hide` 只丢弃"有正面死亡证据"的结果，**不会**因为"探测不可用"而丢弃任何东西。

---

## 6. 对既有工具的取舍

| 工具 | 做法 | 我们的取舍 |
|---|---|---|
| Prowlarr / Jackett | 只抓索引站 advertised seeders，缓存 10–60 分钟，把 `seeders=0` 直接作为过滤条件 | 采纳"索引器健康度"与"最少做种数"概念，但**不把 0 宣称当作死亡**，要求正面证据 |
| qBittorrent / Transmission | 直接用 tracker announce 的 `complete` 作为权威做种数；0 做种视为停滞 | 采纳 complete/incomplete 分离，作为 network 探测的最高优先级证据 |
| Tribler | 用对数刻度衡量 swarm 规模 | 采纳对数饱和做法（sat 在 4 个对等端饱和） |
| BTDigg | 自建 DHT 爬虫，维护每个 infohash 的 `last_seen` 与对等端数 | **采纳其数据模型，但通过其 HTTPS API 消费**——本机 UDP 被封，自建 DHT 不可行。注意：DHT 覆盖不全，**DHT=0 绝不可单独作为死亡证据** |
| seedsearch.io | 暴露每个 infohash 的 `last seen` | 作为 tracker 不可达时的替代信号：30 分钟内=alive，6 小时内=cold，超过 24 小时=unknown |
| Mikan Project | 区分"做种数"与"下载数"，标注"更新中/停止更新" | 采纳"仍在更新 vs 已停止"的区分思路 |

---

## 7. 已知失败模式与缓解

1. **索引站宣称的做种数是陈旧或伪造的** → 置信度门控 + 多引擎交叉佐证 + 饱和曲线 + 可选网络探测
2. **误判死链** → 默认只丢弃有正面死亡证据的结果；纯元数据判死需"≥2 引擎一致 + ≥7 天"；`--dead-filter off` 可完全关闭
3. **单一大型中文站主导排序** → 固定刻度的对数项（不随结果集漂移）+ 每引擎等权 + 不做地域加权 + 海外引擎独立代理
4. **相关度错误：错误的集数/画质版本压过正确版本** → 锚点硬门 + 画质/语言过滤 + 分辨率档位多样性上限
5. **同一资源在 12 个引擎重复刷屏** → info hash 合并 + 簇内条数上限
6. **探测不可达被误当成死亡** → Unknown ≠ Dead，lifeAxis 下限 0.15，`--no-probe` 显式元数据模式
