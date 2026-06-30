# Vendored: TRON protobuf types

Source: `github.com/fbsobreira/gotron-sdk@v0.26.0`, `pkg/proto/{api,core,util}`.

These are the generated protobuf **message types** for TRON chain objects, contracts,
P2P messages, and the Wallet/Solidity/Database/Monitor API messages. They define the
exact wire format and are reused verbatim so go-tron stays byte-compatible with java-tron.

## Modifications applied
- Go import paths rewritten `github.com/fbsobreira/gotron-sdk/pkg/...` →
  `github.com/Redchar1992/go-tron/internal/...` (quote-anchored; the paths embedded in
  protobuf `rawDesc` byte blobs are length-prefixed metadata and were left untouched).
- `*_test.go` removed.
- **gRPC service stubs (`api_grpc.pb.go`, `zksnark_grpc.pb.go`) removed** — the gRPC
  *server* belongs to the M5 API layer; M1 only needs the message types. They will be
  regenerated (or re-vendored) when the API layer lands, pulling `google.golang.org/grpc`
  at that point.

## Do not hand-edit
Treat as generated. To update, re-vendor from a pinned gotron-sdk version and re-apply
the import rewrite.
