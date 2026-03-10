# img-resize-cache

Cloudflare Tunnel から到達する `imgproxy-svc` に対し、`http://imgproxy-svc/<image-url>` でアクセスすると **最大 2048x2048（fit）** にリサイズされた画像を返します。

このリポジトリは **参照用テンプレ** です。実際にデプロイするマニフェスト（環境別の値、Secret名、イメージタグ等）は別リポジトリ側で `kustomize` の overlay として管理し、このテンプレを取り込んで利用してください。

## 構成
- **nginx**: `/<image-url>` を imgproxy に転送し、`proxy_cache` を **tmpfs(最大512Mi)** に保存
- **authz (Go)**: nginx `auth_request` から呼び出され、URLの妥当性を検証（NGは **400**）
- **imgproxy**: 画像の取得とリサイズ（`unsafe/rs:fit:2048:2048` 固定）
- **ssh-socks**: SSH の dynamic port forward により SOCKS5 を提供（imgproxy の外向き通信はこれ経由）

## 事前準備
- 既存 Secret（例: `ssh-socks-secret`）に以下を含めてください
  - `id_rsa`
  - `known_hosts`
- 値の差し替えは **`kustomize/overlays` のパッチ**で行ってください（例: `kustomize/overlays/example`）
  - `SSH_HOST`, `SSH_USER`, `SSH_PORT`
  - `secretName: ssh-socks-secret`
  - `authz` コンテナの `image:`

## 適用
```bash
# 参照用（そのまま適用できるが、値はテンプレのまま）
kubectl apply -k kustomize/base

# 参照用の差分例（実デプロイは別repoで overlay を作る想定）
kubectl apply -k kustomize/overlays/example
```

## 使い方
例:
- `GET http://imgproxy-svc/https://example.com/a.jpg`

注意:
- 元URLに `?query` がある場合、`?` は HTTP のクエリ区切りとして解釈されるため、**パス側に含めるにはURLエンコード**してください。\n  例: `https://example.com/a.jpg?x=1` → `http://imgproxy-svc/https://example.com/a.jpg%3Fx%3D1`

## 動作確認
### 1) 疎通
```bash
kubectl run -it --rm curl --image=curlimages/curl --restart=Never -- \
  curl -I "http://imgproxy-svc/https://example.com/a.jpg"
```

### 2) キャッシュHIT確認
同じURLを2回叩き、レスポンスヘッダ `X-Cache-Status` が `MISS` → `HIT` になることを確認します。

### 3) レートリミット（6 req/min, pod単位）
短時間に連続リクエストして `429` が返ることを確認します。\n（burst を 6 にしているので、最初の数回は通る場合があります）

### 4) SOCKS強制
`ssh-socks` コンテナが落ちた状態で、外部画像取得が失敗する（5xx）ことを確認します。\n（= imgproxy が直接外へ出られていない）

### 5) URL検証（NGは400）
以下の条件を満たさない場合、nginx は **400** を返します。
- `https://` で始まるURLであること
- ドメイン解決結果が **ローカル/プライベートIPでない**こと（解決された **全IPがpublic**）
- パストラバーサル相当のURLでないこと（`..` や代表的なエンコードを拒否）

例（期待: 400）:
- `http://imgproxy-svc/http://example.com/a.jpg`（httpsでない）
- `http://imgproxy-svc/https://127.0.0.1/a.jpg`（loopback）
- `http://imgproxy-svc/https://10.0.0.1/a.jpg`（private）
- `http://imgproxy-svc/https://example.com/%2e%2e/%2e%2e/etc/passwd`（traversal）

