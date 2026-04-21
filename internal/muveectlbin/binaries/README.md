# muveectl embedded binaries

This directory holds gzipped, cross-compiled `muveectl` binaries that the hub
serves from `/api/muveectl/<asset>`. Files are named:

    muveectl_<os>_<arch>[.exe].gz

Populated by the release workflow (`.github/workflows/release.yml`) before
building the muvee server binary. Also producible locally with:

    make muveectl-binaries

When empty (typical for local dev builds), the hub responds to
`/api/muveectl/<asset>` with a 302 redirect to the matching GitHub release
asset instead of serving an embedded copy.

The `.gz` files are gitignored on purpose — they're generated artifacts.
