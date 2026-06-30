// Package types holds TRON chain object types and the protobuf wire format.
//
// Plan: vendor (pinned) the generated protobuf from github.com/fbsobreira/gotron-sdk
// (pkg/proto/core + pkg/proto/api) so we reuse the exact wire definitions rather than
// re-deriving them. CONSENSUS-CRITICAL: serialization must match java-tron byte-for-byte.
// M0: placeholder.
package types
