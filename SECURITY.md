# Security Policy

## Reporting a Vulnerability

Please report vulnerabilities privately via
**[GitHub Security Advisories](../../security/advisories/new)**
("Report a vulnerability" on this repository's Security tab).
Do **not** open a public Issue for security problems.

This is a spare-time project, so acknowledgement and fixes happen on an irregular
schedule, but security reports are treated with the highest priority among all issues.

## Supported Versions

Only the latest release is supported. Older releases do not receive fixes.

## Design Notes

PalaTerm is designed for use inside closed networks:

- No telemetry, no auto-update, no external communication of any kind
- Credentials are stored only in an AES-256-GCM encrypted vault protected by a
  master password (scrypt key derivation)
- SSH host keys are verified on a trust-on-first-use (TOFU) basis

The full security review record (currently in Japanese) is in
[docs/SECURITY.md](docs/SECURITY.md).

---

## 脆弱性の報告（日本語）

脆弱性は公開Issueではなく、本リポジトリの Security タブ →
**[GitHub Security Advisories](../../security/advisories/new)**（Report a vulnerability）
から非公開で報告してください。個人開発のため対応は不定期ですが、
セキュリティ報告は全Issueの中で最優先で扱います。
