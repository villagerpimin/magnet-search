package main

import (
	"fmt"
)

// usage 打印帮助信息。
func usage() {
	fmt.Printf(`msearch %s — 磁力搜索聚合器

用法:
  msearch [选项] <关键词...>     并行检索多个引擎，合并去重后按存活度排序
  msearch serve [选项]          启动本地 Web 界面（默认 http://127.0.0.1:8080）
  msearch check [选项]          自检：逐个测试引擎在当前网络下是否可用
  msearch engines               列出全部引擎及其区域
  msearch version               打印版本

检索选项:
  --engine list        指定引擎（逗号分隔，默认全部）
  --limit n            输出条数（默认 20）
  --per-engine-limit n 每引擎取回的条数（默认 50）
  --category c         分类过滤，由各引擎自行解释
  --dead-filter mode   死链处理：off|hide|mark|strict（默认 hide）
  --min-peers n        丢弃可信对等端少于 n 的结果
  --min-confidence f   丢弃置信度低于 f 的结果
  --rare-quota n       为冷门资源预留的顶部席位（默认按输出条数的 20%%，0 关闭）
  --rare-max-peers n   判定为"稀有"的对等端上界（默认 4）
  --per-cluster n      同一聚类最多输出几条（默认 2）
  --cluster-key mode   聚类粒度：resolution|full|none
  --no-probe           只用引擎元数据，不做 tracker/DHT 探测

网络选项:
  --proxy url[,url]    全局代理，可多个，按顺序回退
  --proxy-engine e=url 指定引擎使用特定代理，可重复
  --timeout dur        单次 HTTP 请求超时（默认 15s）
  --engine-timeout dur 单引擎墙钟预算（默认 25s）
  --retries n          单次请求的重试次数（默认 2）

Web 选项:
  --addr host:port     监听地址（默认 127.0.0.1:8080）

数据库与登录:
  --db-dsn dsn         MySQL 连接串，配置后启用检索记录与登录（也可用 MSEARCH_DB_DSN）
  --db off             关闭数据库（即使配置了连接串）
  --username name      启动时设置/更新登录账号
  --password pass      账号密码（也可用 MSEARCH_PASSWORD，避免留在命令历史里）

输出选项:
  --json               以 JSON 输出
  --quiet              只输出磁力链接
  --no-color           关闭颜色输出
  --verbose            输出调试信息

完整设计见 docs/SPEC.md
`, version)
}
