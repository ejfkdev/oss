# oss

[![ci](https://github.com/ejfkdev/oss/actions/workflows/ci.yml/badge.svg)](https://github.com/ejfkdev/oss/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/ejfkdev/oss)](https://github.com/ejfkdev/oss/releases)

**中文 | [English](README.en.md)**

基于 S3 协议的跨云对象存储命令行工具。一个二进制，操作主流云厂商的对象存储：
AWS S3、阿里云 OSS、腾讯云 COS、华为云 OBS、七牛云 Kodo、Google Cloud Storage、
Cloudflare R2、Wasabi、DigitalOcean Spaces、Backblaze B2、MinIO 及其它 S3 兼容服务。

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
- 🎨 **终端友好**：交互式终端自动彩色高亮，管道/脚本场景自动输出纯文本（`--color auto|always|never`）
- 🌍 **中英文帮助**：自动识别系统语言，中文环境显示中文帮助，其余显示英文（可用 `OSS_LANG=zh|en` 强制）
- 📄 **大桶优化**：流式分页列举（常数内存）、NDJSON 流式输出、有界并发下载，百万级对象不占内存
- 🚀 **并行传输**：多文件并发 + 单文件分片并发，进度条、断点跳过、`.part` 原子落盘
- 🛠 **网络可控**：`-x` 代理、`-H` 自定义头（UA / Cookie …）、`-k` 跳过证书校验

## 安装

**方式一：下载预编译二进制**（推荐）

从 [GitHub Releases](https://github.com/ejfkdev/oss/releases) 下载对应平台的压缩包
（`linux/darwin/windows × amd64/arm64`），解压即用。Release 产物已剥离符号表，
Linux/Windows 版本经 UPX 压缩（macOS 版不压缩：UPX 打包的 Mach-O 会被内核拦截）。
每个版本附带 `SHA256` 校验和文件。

**方式二：源码编译**（需要 Go 1.24+）：

```bash
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

# 腾讯云 COS / 华为云 OBS / 七牛
oss ls s3://bucket-1250000000 --provider tencent --region ap-guangzhou
oss ls https://bucket.obs.cn-north-4.myhuaweicloud.com/prefix/
oss ls s3://mybucket --provider qiniu --region cn-east-1

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
| 七牛云 | `bucket.s3-cn-east-1.qiniucs.com` |
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

## 全局连接参数

所有子命令均支持：

| 参数 | 说明 |
|---|---|
| `--ak` / `--sk` / `--token` | 静态凭证 / STS session token |
| `--profile` | AWS 共享配置 profile（支持 assume-role） |
| `--anonymous` | 强制匿名 |
| `--provider` | `aws\|aliyun\|tencent\|huawei\|qiniu\|gcs\|r2\|wasabi\|spaces\|b2\|minio` |
| `-e/--endpoint` | 自定义 endpoint（覆盖厂商默认） |
| `--region` | 区域（覆盖 URL 推导值） |
| `--path-style` | 强制 path-style 寻址 |
| `--bucket` | URL 无法识别桶名时显式指定 |
| `-x/--proxy` | HTTP 代理，如 `http://127.0.0.1:7890` |
| `-H/--header` | 额外 HTTP 头，可重复：`-H "User-Agent: …" -H "Cookie: …"` |
| `-k/--insecure` | 跳过 TLS 证书校验 |
| `--timeout` | 单请求超时，如 `30s`（默认不限时，适合大文件流式传输） |

## 大桶性能设计

- **流式列举**：逐页拉取、逐条输出，`--all` 时内存占用恒定于单页大小（`--page-size`）；
  不会先把百万 key 装进内存再打印
- **NDJSON**：`-j` 每行一个 JSON（sonic 编码），边列举边写管道，随时可中断
- **有界并发下载**：列举流直接喂给固定大小 worker 池（`--jobs`），
  worker 满时列举自动背压阻塞，任务队列不膨胀
- **分片并发**：单个大文件内部再按 `--parallel` 并发拉取分片（multipart）
- **原子落盘**：先写 `*.part` 再 rename，中断不会留下半个文件
- **v2 → v1 自适应**：服务端不支持 ListObjectsV2 时自动退回 marker 翻页

## 开发

```bash
make test       # 单元测试（URL 解析 / provider / header）
make fmt
make tidy
```

主要依赖：[aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2)、
[sonic](https://github.com/bytedance/sonic)、[urfave/cli v3](https://github.com/urfave/cli)、
[progressbar/v3](https://github.com/schollz/progressbar)、
[go-humanize](https://github.com/dustin/go-humanize)、[errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup)。
