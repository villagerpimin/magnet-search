# msearch — 磁力搜索聚合器

并行检索多个种子/磁力搜索站，合并去重后按**存活度**排序输出。

针对的痛点：

| 痛点 | 做法 |
|---|---|
| 冷门资源容易搜到 | 顶部为"冷门但确实活着"的资源预留席位（`--rare-quota`），并用饱和曲线压制热门度对分数的统治 |
| 优先活跃磁力 | 三级证据（tracker 探测 > DHT 探测 > 引擎宣称）算置信度，分数由**置信度**主导而非做种数 |
| 可过滤死链 | 只有拿到**正面死亡证据**才判死；"测不到"记为未知，绝不误杀 |
| 资源包含海外内容 | 引擎分区域标注，境外引擎自动走代理；RSS 接口优先，绕开 Cloudflare 的 JS 挑战 |

---

## 构建与运行

```bash
cd magnet-search
go build -o msearch ./cmd/msearch

./msearch ubuntu 24.04            # 搜索
./msearch --limit 5 --json ubuntu  # JSON 输出
./msearch check                    # 自检：各引擎在当前网络下是否可用
./msearch engines                  # 列出全部引擎
```

## Web 界面

```bash
./msearch serve                       # 默认 http://127.0.0.1:8080
./msearch serve --addr 127.0.0.1:9000 --proxy http://127.0.0.1:7890
```

浏览器打开后即可使用：顶部搜索框，右侧引擎状态点（点击"检测"逐个探测可达性），
下方筛选条可切换引擎、生效死链过滤策略、设最少对等端数、冷门席位配额与每簇条数。

**流式返回**：搜索与引擎自检都走 SSE，**拿到一个就出一个**，不用等最慢的引擎：

- 检索过程中结果逐条上屏，状态栏实时显示各引擎进度（`apibay 100 · rutor 34 · solidtorrents 失败`）；
- 引擎自检同理，谁先返回先点亮谁的状态点，"可用 n / 共 m"实时跳动；
- 中途只显示**已通过最终判定**的条目（死链、最少对等端、最低置信度当场剔除），
  不会先给用户一个随后会被丢弃的结果；
- 全部引擎结束并完成存活探测后，再按最终分数与死链策略重排一次并给出分页。

翻页走普通接口，服务端有 5 分钟的结果缓存，翻页是秒回。

**代理**：服务启动时带的 `--proxy` 会**自动填进界面的代理框**，开箱即用；
界面上可随时改（留空 = 不用代理），并选择作用范围——「仅境外引擎」或「全部引擎」，
点「应用」保存到服务端。也可以用命令行 `--proxy-engine bt4g=http://...` 单独指定某个引擎。

**引擎默认全选**，界面上第一个「全部」开关可一键全选/全不选；
引擎选择与每页条数会记在浏览器本地，下次打开保持。

筛选条上每个参数名后面的 `?` 都有说明（鼠标悬停即出），其中两个最容易误解：

**两个容易误解的参数**

- **最少对等端**（`--min-peers`）：低于这个数的资源直接丢弃，0 表示不限制。这里的"可信对等端"
  不是页面宣称的做种数，而是综合 tracker 探测、DHT 与多引擎佐证后，对**当前真正能一起下载的人数**
  的估计；测不到时记为"未知"而不是 0，所以它不会误杀那些探测通道不可用、但实际有人在做的冷门资源。
- **冷门席位**（`--rare-quota`）：为"冷门但确实活着"的资源在顶部预留的席位数量，默认是输出条数的 20%。
  冷门指只剩 1–2 个做种者的资源。没有这个配额，1000 个做种的热门资源会把前 20 名占满，
  冷门资源永远露不出来——这正是这个工具要解决的问题。设 0 可关闭。

**分页**：每页条数可选 10 / 20 / 50 / 100，底部有页码导航（首页、上一页、页码窗口、下一页、末页）
与"共 N 条 · 第 X/Y 页"。翻页命中服务端的结果缓存（5 分钟），不会重新跑一遍网络检索。

**代理**：筛选条右侧可直接填代理地址（留空表示不用），并选择作用范围
——「仅境外引擎」（境内与通用引擎直连，失败再走代理，推荐）或「全部引擎」。
点「应用」保存到服务端，之后的检索与自检都按此设置；也可以用命令行
`--proxy http://127.0.0.1:7890` 或 `--proxy-engine bt4g=http://...` 指定。

结果卡片给出**存活状态徽章**（活跃 / 冷门 / 未知 / 死链）、可信对等端数、相关度、
来源引擎与来源标签；右侧色条把排序分拆成"相关度 / 存活证据 / 群体规模 / 新鲜度"四段，
点"详情"可看到 info hash、全部 tracker 与分项数值；点"复制磁力"直接复制磁力链接。
快捷键：`/` 聚焦搜索框、`Esc` 关闭详情面板。

接口（供二次开发）：

| 接口 | 说明 |
|---|---|
| `GET /api/search?q=...&limit=20&engines=a,b&dead_filter=hide&probe=1` | 搜索，返回排序结果与分数分项 |
| `GET /api/search/stream?...` | 同上，但以 SSE 流式推送（`engine` / `result` / `done` 事件） |
| `GET /api/engines` | 引擎清单 |
| `GET /api/check` | 逐个引擎的可达性自检（并发） |
| `GET /api/check/stream` | 同上，但以 SSE 逐个推送引擎状态 |
| `GET /api/settings` | 读取当前代理设置 |
| `POST /api/settings` | 保存代理设置，body 为 `{"proxy":"…","proxy_scope":"region\|all"}` |

搜索接口另外支持 `per_page`（每页条数，默认 20）与 `page`（页码，从 1 开始），
返回体含 `total`、`page`、`per_page`、`total_pages`、`pool_size`。

> 服务默认只监听回环地址。用 `--addr 0.0.0.0:8080` 对外开放时，服务本身没有鉴权，
> 请自行放在反向代理与访问控制之后。

## 跨平台编译

Go 交叉编译出的二进制可以直接在对应系统运行，**Windows 上不需要装 Go，也不需要任何运行时或 VC++ 运行库**
（构建时用 `CGO_ENABLED=0` 静态链接，本项目只依赖纯 Go 的 `golang.org/x/net`）。

```bash
# 一键构建全部平台到 dist/
scripts/build.sh

# 只构建 Windows
scripts/build.sh windows

# 或者手工交叉编译
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o msearch.exe ./cmd/msearch
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -o msearch-arm64.exe ./cmd/msearch
```

产物（`-trimpath -ldflags "-s -w"` 后约 7 MB）：

| 平台 | 文件名 |
|---|---|
| Windows x64 | `msearch-windows-am64.exe` |
| Windows ARM64 | `msearch-windows-arm64.exe` |
| Linux x64 / ARM64 | `msearch-linux-amd64` / `msearch-linux-arm64` |
| macOS Intel / Apple Silicon | `msearch-darwin-amd64` / `msearch-darwin-arm64` |

打 `v*` tag 会由 `.github/workflows/release.yml` 自动构建并附到 Release 上。

Windows 上使用时的两点注意：

- **中文输出乱码**：旧版 cmd.exe 默认代码页是 GBK，程序输出的是 UTF-8。执行 `chcp 65001` 或改用
  Windows Terminal 即可。Windows 11 的终端默认已经是 UTF-8。
- **杀毒软件误报**：未签名的 Go 可执行文件偶尔被启发式引擎拦下，加签名或自行放行即可。

## 常用选项

```
--engine list        指定引擎（逗号分隔，默认全部）
--limit n            输出条数（默认 20）
--dead-filter mode   死链处理：off | hide | mark | strict（默认 hide）
--min-peers n        丢弃可信对等端少于 n 的结果（0 = 不限制）
--min-confidence f   丢弃置信度低于 f 的结果
--rare-quota n       冷门席位数量（默认按输出条数的 20%，0 关闭）
--rare-max-peers n   判定为"稀有"的对等端上界（默认 4）
--per-cluster n      同一聚类最多输出几条（默认 2）
--cluster-key mode   聚类粒度：resolution | full | none
--proxy url[,url]    全局代理，可多个，按顺序回退
--proxy-engine e=url 指定引擎使用特定代理，可重复
--no-probe           只用引擎元数据，不做 tracker/DHT 探测
--timeout dur        单次 HTTP 请求超时（默认 15s）
--json / --quiet / --verbose
```

## 内置引擎

| 引擎 | 区域 | 通道 | 说明 |
|---|---|---|---|
| `apibay` | any | JSON API | The Pirate Bay 官方接口，稳定 |
| `bitsearch` | any | HTML | bitsearch.eu，带做种/下载数 |
| `bt4g` | overseas | **RSS** | bt4gprx.com，RSS 端点可绕过 Cloudflare |
| `nyaa` | overseas | RSS / HTML | 动漫资源主力；nyaa.ink 镜像可用 |
| `yts` | overseas | JSON API | 电影，带画质与做种数 |
| `rutor` | overseas | HTML | 俄文站，提供每条 S/L/C 统计 |
| `tokyotosho` | overseas | RSS / HTML | 东京所 tracker，逐条给 S/L/C |
| `solidtorrents` | overseas | HTML | 与 bitsearch 同模板 |
| `knaben` | overseas | HTML | 逐条给出"最近确认时间"，是比做种数更硬的存活信号 |

每个引擎都带**镜像兜底**：主域名不可达时自动按顺序换下一个镜像。例如
`bt4g` → bt4gprx.com / bt4g.org，`yts` → yts.rs / yts.gg / yts.am，
`nyaa` → nyaa.si / nyaa.ink。`msearch check` 会显示每个引擎的实际可用域名。

## 海外资源：代理与解封

实测结论（见 `docs/SPEC.md`）：**nyaa.si、btdig.com、solidtorrents.to、magnetdl.com、1337x.to、yts.mx、anidex.moe、rutracker.org、thepiratebay.org 在境内被 DNS/SNI 阻断**，直接访问会超时或握手失败。

补上 Clash 规则后，**magnetdl.homes、knaben.org、bt4g.org、yts.gg、yts.am** 变得可达，
而 `1337xx.to` / `x1337x.eu` 能过 TLS 但仍被 Cloudflare 挑战。逐个实测的结论：

| 站点 | 状态 | 处理 |
|---|---|---|
| `knaben.org` | 可用 | 已接入引擎 |
| `bt4g.org` | 可用 | 作为 bt4g 的镜像 |
| `yts.gg` / `yts.am` | 可用 | 作为 yts 的镜像 |
| `magnetdl.co` / `.homes` | 搜索页可开，**详情页由 JS 渲染** | 拿不到磁力，暂不接入 |
| `1337xx.to` / `x1337x.eu` | Cloudflare 全站挑战 | 暂不接入 |
| `btdig` / `solidtorrents` / `anidex` / `rutracker` / `nyaa.si` | 仍被阻断 | 等网络环境改善 |

> 判断某个站能不能接入的标准是**能否拿到磁力**，而不是首页能否打开。
> magnetdl 就是一个典型：列表页能解析出标题、大小、做种数，但没有磁力链接，
> 详情页又是 JS 渲染的，纯 HTTP 客户端拿不到 info hash，因此接进来也没有意义。

三种可行做法：

1. **给 Clash 加规则**（最直接），在规则组之前插入：

   ```
   DOMAIN-SUFFIX,nyaa.si,PROXY
   DOMAIN-SUFFIX,sukebei.nyaa.si,PROXY
   DOMAIN-SUFFIX,btdig.com,PROXY
   DOMAIN-SUFFIX,solidtorrents.to,PROXY
   DOMAIN-SUFFIX,magnetdl.com,PROXY
   DOMAIN-SUFFIX,1337x.to,PROXY
   DOMAIN-SUFFIX,yts.mx,PROXY
   DOMAIN-SUFFIX,anidex.moe,PROXY
   DOMAIN-SUFFIX,rutracker.org,PROXY
   DOMAIN-SUFFIX,thepiratebay.org,PROXY
   ```

2. **显式指定代理**：`./msearch --proxy http://127.0.0.1:7890 "search term"`。
   境外引擎会自动优先走代理，境内/通用引擎优先直连，失败再回退到另一条路径。

3. **用可达镜像**：`nyaa.ink`（Nyaa 镜像）、`yts.rs`、`bt4gprx.com` 在本机可直接访问，
   不改任何网络配置也能搜到海外内容。

> 出口节点质量决定成败：实测同一 URL 经代理连续 8 次只有 4 次成功，
> 所以客户端内置了**多代理链 + 指数退避重试**，`--proxy a,b` 可配多个出口轮换。

## 死链过滤怎么工作

存活判定分三级证据，优先级从高到低：

| 证据 | 置信度基准 | 说明 |
|---|---|---|
| tracker 探测 | 0.90（≥2 个 tracker 则 0.95） | HTTP scrape / UDP scrape / announce 的 `complete` |
| DHT 探测 | 0.60 | BEP5 `get_peers` |
| 引擎宣称 | K=1 → 0.45，K=2 → 0.65，K=3 → 0.75，K≥4 → 0.80 | 多个引擎一致佐证比单一引擎可信 |

判定表：

| 条件 | 结果 |
|---|---|
| 做种数 ≥ 3 | 活跃 |
| 做种数 1–2 | 冷门 |
| 做种数 0 但有下载者 | 冷门（种子已失效，随时可能彻底失败） |
| 做种数 0、下载数 0，**且探测成功返回** | 死链（正面证据） |
| 做种数 0，仅引擎宣称，且 ≥2 引擎一致报 0、发布 ≥7 天 | 死链（弱证据） |
| 其余"都是 0"或"没测到" | 未知（不判死） |

排序分：

```
Score = 0.55*相关度 + 0.33*lifeAxis + 0.05*swarmSize + 0.07*新鲜度
lifeAxis  = 0.15 + 0.85 * 置信度^1.6 * sat(做种数)      sat 在 4 个对等端饱和
swarmSize = min(1, log2(1+做种数)/log2(201)) * 置信度
```

**关键效果**：置信度 0.9 的冷门资源（3 做种）得分 **0.181**，
置信度 0.45 的热门资源（500 做种）得分 **0.152** —— 冷门且可信的资源会压过热门但无法证实的资源；
而两者都有佐证时，热门资源仍然领先（0.225 vs 0.155）。

详细公式、阈值依据与失败模式分析见 [`docs/SPEC.md`](docs/SPEC.md)。

## 本机实测的可用性

`./msearch check` 的输出（本机，Clash TUN fake-IP 模式）：

| 通道 | 状态 |
|---|---|
| 搜索引擎 | apibay / bitsearch / rutor / tokyotosho / bt4g / nyaa / yts 均可用 |
| HTTP tracker | 不可达（`tracker.torrentbay.net`、`tracker.gbitt.info`、`tracker.torrent.eu.org` 等全部超时） |
| UDP tracker | 不可达（出站 UDP 被 TUN 代理阻断） |
| DHT | 不可达 |

因此本机默认走**元数据判定**：`msearch` 会先做一次能力探测（<8 秒），
探测不可用时直接降级，不做无谓等待，并在 `--verbose` 下说明原因。
在允许出网的网络里，同样的命令会自动启用真实 tracker/DHT 探测。

## 代码结构

```
cmd/msearch/        命令行入口：参数解析、引擎装配、输出
internal/model/     数据契约：Result / Liveness（Unknown = -1 与真实 0 严格区分）
internal/engine/    搜索引擎适配器：HTML / JSON / RSS 各自一个实现
internal/httpx/     共享 HTTP 客户端：多代理链、重试、同 host 限流
internal/magnet/    磁力链接与 info hash 解析（hex40 与 base32 双支持）
internal/liveness/  存活探测：bencode、UDP/HTTP tracker、DHT、能力自检
internal/aggregate/ 去重合并、分词、相关度、存活判定、排序
docs/SPEC.md        判定与排序的设计规格（含阈值依据与失败模式）
```

## 已知限制

- 搜索引擎的做种数普遍**陈旧或造假**，只能作为弱证据；真正的确认需要 tracker/DHT 通道。
- tracker/DHT 被阻断时无法区分"资源已死"与"没人索引它"，此时判定只到"冷门/活跃"这一层。
- 相关度以英文/数字词元为主，中文查询命中日文或英文标题时依赖**引擎自身的排序位置**兜底
  （引擎的索引里通常带中文别名），跨语种文本相似度不做硬匹配。
- `1337x` 与 `magnetdl` 暂未接入：前者搜索页被 Cloudflare 全站挑战，后者需要先取一次性
  JWT 才能拿到真实搜索地址。
