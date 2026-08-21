# oss

[![ci](https://github.com/ejfkdev/oss/actions/workflows/ci.yml/badge.svg)](https://github.com/ejfkdev/oss/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/ejfkdev/oss)](https://github.com/ejfkdev/oss/releases)

**中文 | [English](README.en.md)**

基于 S3 协议的跨云对象存储命令行工具。一个二进制，操作主流云厂商的对象存储：
AWS S3、阿里云 OSS、腾讯云 COS、华为云 OBS、百度云 BOS、金山云 KS3、
UCloud US3、京东云 OSS、Google Cloud Storage、Cloudflare R2、Scaleway、Wasabi、
DigitalOcean Spaces、Backblaze B2、MinIO 及其它 S3 兼容服务。

- 🌐 **匿名桶浏览**：直接给 URL 即可列出/下载公共读桶，无需任何凭证
- 🔗 **URL 即入口**：支持 `https://...` URL 访问，识别各厂商域名并解析桶名/区域；
  URL 查询参数（`prefix`、`delimiter`、`max-keys`、`continuation-token`、`marker`、`start-after`）
  直接作为过滤条件，类似 [aws-s3-bucket-browser](https://github.com/qoomon/aws-s3-bucket-browser)
- 📁 **目录形式转发的桶**：支持反向代理/CDN 把桶挂载在路径下的场景
  （如 `https://files.example.com/mybucket/`），自动按 path-style 解析，S3 API 透传即可列举下载
- 🎫 **额外参数透传**：URL 上的非列表参数（如 `?token=abc` 鉴权网关）会自动注入到
  每一个 API 请求（列举/下载/上传/预签名），且在 SigV4 签名前注入
- 🔑 **多种认证**：AK/SK、STS 临时凭证（session token）、AWS profile（含 assume-role）、匿名
- 🗂 **列表缓存**：已列举内容默认缓存到本地，重复列举/导出/下载秒开（`-d` 这类需全量扫描的场景提速数百倍）
- 📤 **导出文件列表**：按过滤条件导出为 `txt / csv / xlsx / yaml / markdown`（`--export`）
- ⬇️ **筛选批量下载**：`cp -r` 按 `--include/--exclude/--prefix` 下载所有匹配文件，保持 key 目录结构
- 🔍 **桶归属查找**：`find` 批量（命令行多个/标准输入）探测桶在哪个云存储，
  支持桶名与完整桶 URL；匿名探测并同时识别**能否匿名列目录**，可匿名列目录的桶
  命令行亮绿 ★ 高亮，`--export` 导出含专门 `listable_url` 字段的 txt/csv/xlsx/yaml/md
- 🔌 **对外服务**：`oss serve` 一个端口提供 REST API + OpenAPI 3 文档 + MCP 工具端点，
  `oss mcp stdio|http|sse` 把 ls/stat/cat/presign/find 作为 MCP 工具供 Claude 等客户端调用
- 🎨 **终端友好**：交互式终端自动彩色高亮，管道/脚本场景自动输出纯文本（`--color auto|always|never`）
- 🌍 **中英文帮助**：自动识别系统语言，中文环境显示中文帮助，其余显示英文（可用 `OSS_LANG=zh|en` 强制）
- 📄 **大桶优化**：流式分页列举（常数内存）、NDJSON 流式输出、有界并发下载，百万级对象不占内存
- 🚀 **并行传输**：多文件并发 + 单文件分片并发，进度条、断点跳过、`.part` 原子落盘
- 🛠 **网络可控**：`-x` 代理、`-H` 自定义头（UA / Cookie …）、`-k` 跳过证书校验

## 安装

**方式一：Homebrew**（macOS / Linux，推荐）

```bash
brew install ejfkdev/tap/oss
```

**方式二：go install**（需要 Go 1.24+）

```bash
go install github.com/ejfkdev/oss@latest
```

**方式三：预编译二进制**

从 [GitHub Releases](https://github.com/ejfkdev/oss/releases) 下载对应平台的压缩包
（`linux/darwin/windows × amd64/arm64`），解压即用。Release 产物已剥离符号表，
Linux/Windows 版本经 UPX 压缩（macOS 版不压缩：UPX 打包的 Mach-O 会被内核拦截）。
每个版本附带 `SHA256` 校验和文件。

**方式四：源码编译**（需要 Go 1.24+）：

```bash
git clone https://github.com/ejfkdev/oss && cd oss
make build      # 产物为 ./oss
make install    # 安装到 $GOPATH/bin
make release    # 本地交叉编译全部平台到 dist/
```

## 快速开始

```bash
# 匿名浏览公共桶（无需凭证）
oss ls https://noaa-nwm-pds.s3.amazonaws.com/?delimiter=/
oss ls "https://noaa-nwm-pds.s3.amazonaws.com/?prefix=nwm.20250101/&delimiter=/&max-keys=20"
oss cp -r https://noaa-nwm-pds.s3.amazonaws.com/nwm.20250101/ ./nwm/ --jobs 32

# 阿里云 OSS
oss ls oss://mybucket/logs/ --ak <AK> --sk <SK> --region cn-hangzhou
export OSS_ACCESS_KEY_ID=xxx OSS_SECRET_ACCESS_KEY=yyy   # 或者用环境变量
oss ls mybucket --provider aliyun                         # 裸桶名写法

# 腾讯云 COS / 华为云 OBS
oss ls s3://bucket-1250000000 --provider tencent --region ap-guangzhou
oss ls https://bucket.obs.cn-north-4.myhuaweicloud.com/prefix/

# Yandex / Exoscale / Arvan
oss ls s3://mybucket --provider yandex
oss ls s3://mybucket --provider exoscale --region ch-gva-2

# MinIO / 自建 S3
oss ls s3://mybucket -e http://127.0.0.1:9000 --ak minioadmin --sk minioadmin
oss ls http://127.0.0.1:9000/mybucket/prefix/             # IP/端口自动识别为 path-style

# 走代理、自定义 UA / Cookie
oss ls s3://mybucket -x http://127.0.0.1:7890 \
    -H "User-Agent: my-agent/1.0" -H "Cookie: session=abc"

# 目录形式转发的桶（反代把桶挂在路径下，S3 API 透传）
oss ls "https://files.example.com/mybucket/?prefix=2026/&max-keys=10"
oss cp "https://files.example.com/mybucket/<key>" ./

# 附带额外参数的访问（token 等会自动带到每个请求）
oss ls "http://gateway.example.com/bucket?token=abc"
oss cp "http://gateway.example.com/bucket/file.bin?token=abc" ./
```

## Target 写法

| 形式 | 示例 |
|---|---|
| scheme | `s3://bucket/prefix`、`oss://`、`cos://`、`obs://` |
| 裸桶名 | `mybucket/prefix`（配合 `--provider` / `-e`） |
| HTTP URL | `https://bucket.s3.us-east-1.amazonaws.com/prefix?prefix=logs/` |

URL 自动识别的厂商域名（解析出 endpoint / region / bucket）：

| 厂商 | 域名示例 |
|---|---|
| AWS | `bucket.s3.region.amazonaws.com`、`s3.region.amazonaws.com/bucket` |
| 阿里云 | `bucket.oss-cn-hangzhou.aliyuncs.com`（含 accelerate） |
| 腾讯云 | `bucket-appid.cos.ap-guangzhou.myqcloud.com` |
| 华为云 | `bucket.obs.cn-north-4.myhuaweicloud.com` |
| 百度云 BOS | `bucket.s3.bj.bcebos.com`、`s3.bj.bcebos.com/bucket` |
| 金山云 KS3 | `bucket.ks3-cn-beijing.ksyuncs.com` |
| UCloud US3 | `bucket.s3-cn-sh2.ufileos.com` |
| 京东云 OSS | `s3.cn-north-1.jdcloud-oss.com/bucket`（path-style） |
| Scaleway | `bucket.s3.fr-par.scw.cloud`（fr-par/nl-ams/pl-waw） |
| Wasabi | `bucket.s3.us-east-1.wasabisys.com`（14 个地域） |
| DigitalOcean Spaces | `nyc3.digitaloceanspaces.com/bucket`（9 个地域） |
| Yandex | `bucket.storage.yandexcloud.net` |
| Exoscale SOS | `bucket.ch-gva-2.sos.exoscale.com`（6 个地域） |
| Arvan Cloud | `bucket.s3.ir-thr-at1.arvanstorage.ir` |
| Cloudflare R2 | `bucket.<account_id>.r2.cloudflarestorage.com` |
| GCS | `storage.googleapis.com/bucket`（HMAC 密钥） |

其它主机（MinIO、Ceph、反代转发桶、CNAME…）统一按 **path-style** 解析：
第一段路径作为桶名，其余为 key，例如
`https://files.example.com/mybucket/sub/key.jpg` → 桶 `mybucket`、key `sub/key.jpg`。
若主机直接指向桶（CNAME），用 `--bucket NAME` 显式指定。URL 为空时（`oss ls`）列举所有桶。

**URL 查询参数分两类**：

- 列表 API 参数（`prefix` `delimiter` `max-keys` `continuation-token` `marker`
  `start-after` `encoding-type` `list-type`）→ 解释为列举过滤条件
- 其余参数（如 `token=abc`）→ 视为额外参数，**透传到对该目标的每个请求**
  （ListObjects / GetObject / PutObject / Presign 等），适用于带鉴权参数的网关；
  参数在 SigV4 签名前注入、参与签名，因此与 AK/SK 凭证可同时使用

## 认证

按以下顺序解析，命中即止：

1. `--ak` / `--sk` / `--token`（STS session token）
2. 环境变量 `OSS_ACCESS_KEY_ID` / `OSS_SECRET_ACCESS_KEY` / `OSS_SESSION_TOKEN`
3. 环境变量 `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN`
4. `--profile NAME` 或 `~/.aws/credentials|config`（支持 assume-role 等 STS profile）
5. 都没有 → **匿名访问**（`--anonymous` 可强制）

## 命令

### `oss ls` — 列举桶/对象

```bash
oss ls                                      # 列举所有桶
oss ls s3://b/dir/ --limit 50               # 默认目录式（delimiter=/），首页 50 条
oss ls s3://b/dir/ --limit 50               # 再次运行自动从断点继续（token 已缓存）
oss ls s3://b/dir/ --reset                  # 忽略缓存，从头列举
oss ls s3://b/dir/ --next-token <t>         # 显式指定 token（覆盖缓存）
oss ls s3://b/dir/ -d                       # 只列目录（PRE=目录/公共前缀）
oss ls s3://b/dir/ -f --include "*.gz"      # 只列文件，glob 过滤
oss ls s3://b --all                         # 流式列举全部（常数内存）
oss ls s3://b -r                            # 平铺所有 key（不分目录）
oss ls s3://b --all -j > list.ndjson        # NDJSON 流式输出，适合管道处理
oss ls s3://b --export list.xlsx            # 导出文件列表为 Excel
```

ls 只负责"看"（列举/过滤/导出列表）；**下载/上传请使用 `cp`**（见下文）。

参数分类（`oss ls -h` 可见完整说明）：

- **过滤**：`--prefix`、`--delimiter`、`-r/--recursive`、`-d/--dirs`、`-f/--files`、`--include`、`--exclude`
- **分页与缓存**：`-n/--limit`、`--all`、`--page-size`、`--next-token`、`--reset`、`--no-cache`、`--start-after`
- **输出**：`-j/--json`、`--bytes`、`--color auto|always|never`
- **导出**：`--export <file>`（格式按扩展名 `.txt .csv .xlsx .yaml/.yml .md`）

**输出与颜色**：`PRE` 表示公共前缀（目录）。交互式终端下自动美化输出：

- 目录：蓝色加粗（含 `PRE` 标记与目录名）
- 文件大小按量级着色：<1 MiB 绿色、<1 GiB 黄色、≥1 GiB 红色
- 修改时间置灰、`stat` 标签青色对齐（中文标签按双宽度对齐）
- 成功消息绿色 `✓`、截断/续传提示黄色、错误红色
- 所有提示消息随系统语言中英文切换

当输出被管道/重定向给其它程序时自动输出**纯文本**，不掺入任何 ANSI 控制码
（保证脚本可解析）。`--color always|never` 可强制开/关（所有子命令通用）。

**导出**（`--export`）：按过滤条件导出完整文件列表（不受 `-n` 显示条数限制），
含 type/key/size/last_modified/etag/storage_class 字段，格式由扩展名决定。
首次导出会全量列举并写入缓存，之后同样条件的导出/列举直接命中缓存秒开。

**分页与列表缓存**（默认开启）：

- 列举被 `--limit` 截断时，再次运行同一命令即自动从断点继续，无需手写 token；
  `--next-token` 仍可显式指定（此时不使用缓存）
- **已获取的列表内容会缓存在本地**（`~/Library/Caches/ejfkdev/oss/ls-cache.json`，
  Linux 为 `~/.cache/ejfkdev/oss/`），重复列举直接读缓存、不再请求服务端。
  对 `-d` 这类需要扫描大量文件才能找到目录的场景提速明显（实测 17s → 0.03s）
- 缓存按「endpoint+桶+前缀+分隔符」指纹隔离，24 小时过期，单个列举最多缓存
  5 万条（超出自动降级为 token 续传模式，结果仍正确）
- 列举自然结束后，下次运行从完整快照回放；`--reset` 清除缓存重新获取；
  `--no-cache` 完全禁用缓存（实时结果，翻页需手动 `--next-token`）；
  `--all` 流式直出，不使用缓存

### `oss cat` — 输出对象内容

```bash
oss cat s3://b/config.yaml
oss cat s3://b/big.bin --range 0-1023       # 只取一段
```

### `oss stat` — 查看元数据

```bash
oss stat s3://b/dir/file.tar.gz             # 对象：大小/etag/类型/自定义 metadata
oss stat s3://b                             # 桶可达性
```

### `oss cp` — 下载 / 上传 / 跨桶拷贝

```bash
oss cp s3://b/path/file.tar.gz ./           # 下载单文件（分片并发）
oss cp s3://b/path/file.tar.gz -            # 下载到 stdout
oss cp -r s3://b/logs/ ./logs --include "*.gz"   # 按条件批量下载
oss cp -r s3://b/dataset/ ./data --jobs 32 --parallel 8 \
        --exclude "*.tmp" --skip-existing   # 递归下载，glob 过滤
oss cp ./dist s3://b/release/ -r            # 递归上传目录
oss cp ./a.bin s3://b/b.bin                 # 单文件上传（自动分片）
oss cp s3://b1/k s3://b2/k                  # 服务端拷贝（同 endpoint）
```

常用参数：`-r`、`--jobs`（并发文件数，默认 16）、`--parallel`（单文件分片数，默认 5）、
`--include` / `--exclude`（glob，可重复）、`--skip-existing`、`--no-progress`。

目录结构：`-r` 下载时按 key 中的 `/` 逐级创建本地目录；源以 `/` 结尾时其内容
直接放入目标目录（如 `s3://b/logs/` → `./logs/<文件>`）。

### `oss presign` — 预签名 URL

```bash
oss presign s3://b/key --expires 1h         # GET 下载链接
oss presign s3://b/key --method PUT         # 上传链接
```

### `oss find` — 桶归属查找

支持批量：命令行给多个参数，和/或从 stdin 一行一个读取（可混用）。每个输入可以是
**桶名**（并发探测所有已知厂商的常用区域）或**完整桶 URL/路径**（只探测该端点，更精确）。

探测方式为向桶发 **ListObjects 请求**（默认匿名；配置凭证时自动改为 SigV4 签名请求，见下），
一次请求同时判断存在性 + 能否列目录：

| 响应 | 判定 |
|---|---|
| HTTP 200 | 存在且**可匿名列目录**（命令行亮绿 ★ 高亮） |
| HTTP 3xx | 存在（重定向） |
| HTTP 401/403 | 存在但私有（不可匿名列） |
| HTTP 404 / 域名不存在 | 不存在 |
| 超时 / 其它状态码 | 无法判断 |

```bash
oss find mybucket                       # 查找单个桶名
oss find bucket-a bucket-b bucket-c     # 批量（命令行多个）
cat buckets.txt | oss find              # 批量（stdin 一行一个）
oss find https://mybucket.s3.us-east-1.amazonaws.com/   # 完整 URL
oss find mybucket --listable            # 只列出可匿名列目录的桶
oss find mybucket --region cn-beijing   # 只探测指定区域
oss find bucket-a bucket-b --export r.csv   # 导出 CSV
oss find mybucket --provider aliyun --ak LTAI... --sk ...   # 用凭证验证私有桶
```

**输出形态**：只打印命中的结果，发现一个命中立即**流式打印一行**（含完整访问 URL），
未命中/无法判断的探测不显示。**两种模式**：默认「发现桶存储」——桶存在即命中（含私有）；
`--listable`「发现可匿名列目录的桶」——只输出可匿名列目录的命中。

**凭证探测**：默认匿名探测、不发送任何凭证；配置了凭证（`--ak/--sk`、`OSS_*`/`AWS_*`
环境变量或 `--profile`）时自动切换为 **SigV4 签名探测**，可验证非匿名桶；`--anonymous`
强制匿名。凭证本身被拒（`InvalidAccessKeyId` 等）判为无法判断以防误报；只有明确目标
（`--provider` 指定或完整桶 URL）的普通 403 才判为「存在·拒绝访问」。

**导出**（`--export`）：格式按扩展名 `.txt .csv .xlsx .yaml .md`，包含专门字段
`listable_url`，**仅对可匿名列目录的桶**填入其完整访问 URL，便于直接筛选可匿名列举的目标。

输出示例（可匿名列目录的桶亮绿 ★ 高亮）：

```
noaa-nwm-pds
  ★  AWS S3  可匿名列目录  https://noaa-nwm-pds.s3.amazonaws.com

★ 发现 1 个可匿名列目录的桶:
  ★ https://noaa-nwm-pds.s3.amazonaws.com
    → 可直接使用: oss ls "https://noaa-nwm-pds.s3.amazonaws.com?delimiter=/"
```

说明：**默认只探测中国大陆+港台地域**（`--cn` 显式指定同效）；`--global` 探测全部
公网地域（含海外）；`--region` 只探测指定区域。全量地域：阿里云 32、腾讯 21、华为 29、
UCloud 25、Wasabi 14、金山 9、DigitalOcean Spaces 9、百度 7、京东 5、Exoscale 6、
Scaleway 3、Arvan 2、AWS 国际全域+中国 2（`.amazonaws.com.cn`）。腾讯云桶名需含
APPID 后缀（如 `examplebucket-1250000000`）。**不支持七牛**：七牛匿名访问一律返回
400（需鉴权），无法判断桶是否存在或能否匿名列目录，且其 `bkt.clouddn.com` /
`qiniudemo.com` 为绑定桶的 CDN 域名、无法从桶名推导。B2 对所有桶返回 403、R2 需账号 ID，
也不在探测范围。结果为最佳判断，未找到不代表绝对不存在。别名：`which`（桶安全检测命令
规划中，将使用 `scan`/`audit` 名称）。

### `oss serve` / `oss mcp` — 对外服务（REST / OpenAPI / MCP）

基于 [xyz-go](https://github.com/ejfkdev/xyz-go)（一次定义、三个界面），`ls` / `stat` /
`cat` / `presign` / `find` 五个查询型命令同时对外提供 **HTTP REST** 与 **MCP 工具**
两种调用方式。`cat` 在 HTTP 侧输出原始字节流（`GET /cat`）、在 MCP 侧提供 text/base64
工具；仅 `cp`（文件传输）保持 CLI：

```bash
oss serve --addr 127.0.0.1:8080     # 一个端口：REST + OpenAPI + /mcp
oss mcp stdio                        # MCP 工具服务器（stdio，本地客户端）
oss mcp http --addr :9000 --bearer tok    # MCP streamable HTTP（远程）
```

#### `oss serve` — HTTP/JSON 服务

```bash
oss serve --addr 127.0.0.1:8080                       # 默认端口 :8080
oss serve --addr :8443 --tls-cert c.pem --tls-key k.pem --bearer tok1,tok2
```

| 路由 | 用法 |
|---|---|
| `GET /ls` | 列举：`target` 桶/前缀目标（留空列桶），`prefix`、`delimiter`（默认 `/`，空串递归平铺）、`recursive`、`dirs`、`files`、`include`、`exclude` 过滤，`limit`（默认 1000）+ `next_token` 分页，`all=true` 全量 |
| `GET /cat` | 读取对象内容：**原始字节流**（非 JSON），`target` + `range`（如 `0-1023`，与 CLI `--range` 同义）；也接受标准 `Range` 请求头；响应带 `Content-Type`/`Content-Length`，范围读取时 `206` + `Content-Range` |
| `GET /stat` | `target` 桶连接信息（kind=bucket）或对象元数据（kind=object：size/modified/etag/content_type/storage_class/metadata） |
| `GET /presign` | `target` + `method GET\|PUT` + `expires`（如 `15m`）生成预签名 URL；匿名返回 401 |
| `GET /find` | `inputs` 可重复（`inputs=a&inputs=b`）+ `provider`/`region`/`global`/`cn`/`listable`/`jobs`；返回各探测状态与 `anonymous_listable` URL 列表 |
| `GET /openapi.json` | OpenAPI 3.0 文档（含每个字段描述，可直接导入 Postman/Insomnia） |
| `GET /healthz` | 存活探针，返回 `{"status":"ok"}` |

```bash
# 匿名列举公共桶，翻页直到 truncated=false（每页 500 条）
curl -s '127.0.0.1:8080/ls?target=https://noaa-nwm-pds.s3.amazonaws.com/&limit=2'
# {"target":"...","entries":[{"type":"prefix","key":"nwm.20250101/"},...],
#  "shown":2,"next_token":"...","truncated":true}

curl -s '127.0.0.1:8080/stat?target=https://noaa-nwm-pds.s3.amazonaws.com/index.html'
# {"kind":"object",...,"size":31608,"modified":"2023-04-05T16:28:08Z","etag":"...","content_type":"text/html"}

# 原始字节流：整对象下载 / 范围读取（二进制安全，落盘即用）
curl -s '127.0.0.1:8080/cat?target=https://noaa-nwm-pds.s3.amazonaws.com/index.html' \
  -o index.html
curl -sD - '127.0.0.1:8080/cat?target=s3://mybucket/data.bin&range=0-127' -o part.bin
# HTTP/1.1 206 Partial Content
# Content-Range: bytes 0-127/14334567
```

**凭证**：凭证走请求头（不进 URL，避免进日志），逐个请求传入；未传时回落服务进程的
环境变量 / `~/.aws` 共享配置：

```bash
curl -s -H 'X-Oss-Ak: LTAI...' -H 'X-Oss-Sk: ...' \
  '127.0.0.1:8080/stat?target=oss://mybucket/file.gz&provider=aliyun'
```

会话型（STS 凭证）再加 `X-Oss-Token`。所有公共连接参数（`provider`/`endpoint`/`region`/
`path_style`/`proxy`/`headers`/`insecure`/`timeout`）都可作为同名 query 参数或 headers 传入。

**错误**：统一 `{"error":"..."}`，状态码映射按同一分类：参数/校验类 400、凭证无效或未提供
401、权限不足 403、桶/对象不存在 404、上游不可达 503，展示异常 500。

**安全**：默认**不鉴权**——只监听内部网络或必须配 `--bearer`；`--cors` 控制允许来源；
`--timeout` 限制请求超时；收到 SIGINT/SIGTERM 时优雅关停（在飞请求先排空）。

#### `oss mcp` — MCP 工具服务器

把 `ls` / `stat` / `cat` / `presign` / `find` 暴露为 MCP 工具（工具名即命令名，带
read-only 标注与 inputSchema），供支持 MCP 的客户端编排调用：

```bash
oss mcp stdio                       # 本地进程标准输入/输出（本地客户端推荐）
oss mcp http --addr :9000 --bearer tok    # streamable HTTP，供远程客户端
oss mcp sse --addr 127.0.0.1:9000         # SSE 传输
oss mcp stdio --versions 2024-11-05,2025-06-18   # 限定协议版本
```

客户端配置示例（Claude Desktop 的 `claude_desktop_config.json`）：

```json
{
  "mcpServers": {
    "oss": {
      "command": "oss",
      "args": ["mcp", "stdio"]
    }
  }
}
```

MCP 工具调用约定：连接类与目标类参数和 HTTP 版完全一致（凭证直接作为工具参数
`ak`/`sk`/`token` 传入）；远程（http/sse）用 `--bearer` 加令牌。`tools/list` 返回
五个工具；`tools/call` 失败时以 `isError: true` 携带与 HTTP 相同的分类错误消息。
`cat` 工具读取对象内容：UTF-8 文本放 `text` 字段、二进制放 `base64` 字段（二选一），
单次上限 16MiB——更大的对象用 HTTP `GET /cat`（原始字节流）或 CLI `oss cat`。

## 全局连接参数

所有子命令均支持：

| 参数 | 说明 |
|---|---|
| `--ak` / `--sk` / `--token` | 静态凭证 / STS session token |
| `--profile` | AWS 共享配置 profile（支持 assume-role） |
| `--anonymous` | 强制匿名 |
| `--provider` | `aliyun\|arvan\|aws\|b2\|baidu\|exoscale\|gcs\|huawei\|jdcloud\|ks3\|minio\|r2\|scaleway\|spaces\|tencent\|ucloud\|wasabi\|yandex` |
| `-e/--endpoint` | 自定义 endpoint（覆盖厂商默认） |
| `--region` | 区域（覆盖 URL 推导值） |
| `--path-style` | 强制 path-style 寻址 |
| `--bucket` | URL 无法识别桶名时显式指定 |
| `-x/--proxy` | HTTP 代理，如 `http://127.0.0.1:7890` |
| `-H/--header` | 额外 HTTP 头，可重复：`-H "User-Agent: …" -H "Cookie: …"` |
| `-k/--insecure` | 跳过 TLS 证书校验 |
| `--timeout` | 单请求超时，如 `30s`（默认不限时，适合大文件流式传输） |
| `--color` | 彩色输出 `auto\|always\|never`（auto = 仅交互式终端） |

## 大桶性能设计

- **流式列举**：逐页拉取、逐条输出，`--all` 时内存占用恒定于单页大小（`--page-size`）；
  不会先把百万 key 装进内存再打印
- **NDJSON**：`-j` 每行一个 JSON（sonic 编码），边列举边写管道，随时可中断
- **有界并发下载**：列举流直接喂给固定大小 worker 池（`--jobs`），
  worker 满时列举自动背压阻塞，任务队列不膨胀
- **分片并发**：单个大文件内部再按 `--parallel` 并发拉取分片（multipart）
- **原子落盘**：先写 `*.part` 再 rename，中断不会留下半个文件
- **v2 → v1 自适应**：服务端不支持 ListObjectsV2 时自动退回 marker 翻页

## 命令参数手册

以下为各子命令的完整参数（`oss <command> -h` 可查看同样内容）。

### `oss ls` — 列出桶/对象

| 参数 | 默认 | 说明 |
|---|---|---|
| `--prefix <p>` | | 前缀过滤（与 URL 路径、`?prefix=` 叠加生效） |
| `--delimiter <d>` | `/` | 目录分隔符（目录视图）；设为空串则平铺 |
| `-r, --recursive` | | 平铺列出所有对象（不区分目录） |
| `-d, --dirs` | | 只列目录 |
| `-f, --files` | | 只列文件（对象） |
| `--include <glob>` | | glob 包含过滤，可重复（匹配相对路径或文件名） |
| `--exclude <glob>` | | glob 排除过滤，可重复 |
| `-n, --limit <N>` | `1000` | 最多显示条数；再次运行自动从断点继续 |
| `-a, --all` | | 列出全部（流式输出，常数内存，不使用缓存） |
| `--page-size <N>` | `1000` | 每次 ListObjects 请求的条数 |
| `--next-token <t>` | | 显式指定续传 token（覆盖缓存） |
| `--reset` | | 清除该列举的缓存，重新从服务端获取 |
| `--no-cache` | | 不读写列举缓存（实时结果，翻页需手动 `--next-token`） |
| `--start-after <k>` | | 从指定 key 之后开始列举 |
| `-j, --json` | | NDJSON 输出（每行一条，流式） |
| `--bytes` | | 大小显示为字节数（默认人类可读） |
| `--export <file>` | | 导出筛选后的文件列表；格式按扩展名 `.txt .csv .xlsx .yaml .md` |

### `oss cat` — 输出对象内容

| 参数 | 默认 | 说明 |
|---|---|---|
| `--range <r>` | | 字节范围：`0-1023`、`bytes=0-1023`、`100-`（开区间） |

### `oss stat` — 查看元数据

无专属参数（对象 → 大小/etag/类型/自定义 metadata；桶 → 可达性与连接信息）。

### `oss cp` — 下载 / 上传 / 跨桶拷贝

| 参数 | 默认 | 说明 |
|---|---|---|
| `-r, --recursive` | | 按前缀/目录递归拷贝 |
| `--include <glob>` | | glob 包含过滤，可重复 |
| `--exclude <glob>` | | glob 排除过滤，可重复（如 `*.tmp`） |
| `--skip-existing` | | 目标已存在则跳过 |
| `--jobs <N>` | `16` | 并发文件数 |
| `--parallel <N>` | `5` | 单文件分片并发数（multipart） |
| `--no-progress` | | 关闭进度显示 |

### `oss presign` — 预签名 URL

| 参数 | 默认 | 说明 |
|---|---|---|
| `--expires <d>` | `15m` | 链接有效期，如 `15m`、`1h` |
| `--method <m>` | `GET` | 签名的 HTTP 方法：`GET`（下载）\| `PUT`（上传） |

### `oss find` — 桶归属查找

| 参数 | 默认 | 说明 |
|---|---|---|
| `--cn` | 默认行为 | 只探测中国大陆+港台地域 |
| `--global` | | 探测全部地域（含海外） |
| `--listable, -l` | | 只输出可匿名列目录的桶（命中即流式打印；`-j`/`--export` 也跟随过滤） |
| `--region <R>` | | 只探测指定区域（覆盖 cn/global） |
| `--jobs <N>` | 全部并发 | 并发探测数 |
| `-j, --json` | | NDJSON 输出（每个探测一行 + 汇总行，含 anonymous_listable 数组与 auth 模式） |
| `--export <file>` | | 导出结果，格式按扩展名 `.txt .csv .xlsx .yaml .md`；含 `listable_url` 字段存可匿名列桶的完整 URL |

### `oss serve` — HTTP 服务

| 参数 | 默认 | 说明 |
|---|---|---|
| `--addr <ADDR>` | `:8080` | 监听地址 |
| `--bearer <TOK>` | 不鉴权 | Bearer 令牌（逗号分隔可多次） |
| `--cors <LIST>` | | CORS 允许来源（`*` 允许任意） |
| `--timeout <d>` | 不限 | 读写/空闲超时（仍保留 10s 请求头超时） |
| `--tls-cert/--tls-key` | | 同时给出时启用 HTTPS |

### `oss mcp` — MCP 工具服务器

| 参数 | 默认 | 说明 |
|---|---|---|
| 传输 | 必填 | `stdio` \| `http` \| `sse`（第一个位置参数） |
| `--addr <ADDR>` | `:8080` | 监听地址（http/sse） |
| `--versions <LIST>` | 全部 | 限定 MCP 协议版本（逗号分隔） |
| `--bearer/--cors` | | 同 serve |
| `--name/--server-version` | `oss`/版本号 | 服务器标识 |
| `--stateless/--json-response` | | 流式 HTTP 无状态 / JSON 应答 |
| `--session-timeout <d>` | 不限 | 会话空闲超时 |

### 公共连接参数（所有子命令）

| 参数 | 说明 |
|---|---|
| `--ak <ID>` | AccessKey ID（环境变量 `OSS_ACCESS_KEY_ID` / `AWS_ACCESS_KEY_ID`） |
| `--sk <SECRET>` | AccessKey Secret（环境变量 `OSS_SECRET_ACCESS_KEY` / `AWS_SECRET_ACCESS_KEY`） |
| `--token <T>` | STS 会话令牌（环境变量 `OSS_SESSION_TOKEN` / `AWS_SESSION_TOKEN`） |
| `--profile <NAME>` | AWS 共享配置 profile（`~/.aws/config`，支持 assume-role） |
| `--anonymous` | 强制匿名访问 |
| `--provider <P>` | 云厂商：`aliyun arvan aws b2 baidu exoscale gcs huawei jdcloud ks3 minio r2 scaleway spaces tencent ucloud wasabi yandex` |
| `-e, --endpoint <URL>` | 自定义 endpoint（覆盖厂商默认值） |
| `--region <R>` | 区域（覆盖 URL 推导值） |
| `--path-style` | 强制 path-style 寻址（`http://host/bucket/key`） |
| `--bucket <NAME>` | 显式指定桶名（URL 无法识别时） |
| `-x, --proxy <URL>` | HTTP 代理，如 `http://127.0.0.1:7890` |
| `-H, --header "K: V"` | 附加 HTTP 头，可重复（User-Agent、Cookie 等） |
| `-k, --insecure` | 跳过 TLS 证书校验 |
| `--timeout <d>` | 单请求超时，如 `30s`（0 = 不超时） |
| `--color <WHEN>` | 彩色输出：`auto`（默认）\| `always` \| `never` |

## 开发

```bash
make test       # 单元测试（URL 解析 / provider / header）
make fmt
make tidy
```

主要依赖：[aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2)、
[sonic](https://github.com/bytedance/sonic)、[urfave/cli v3](https://github.com/urfave/cli)、
[xyz-go](https://github.com/ejfkdev/xyz-go)（serve/mcp 对外服务）、
[progressbar/v3](https://github.com/schollz/progressbar)、
[go-humanize](https://github.com/dustin/go-humanize)、[errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup)。
