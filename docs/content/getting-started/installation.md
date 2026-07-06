---
title: "Installation"
description: "Homebrew, Scoop, apt, dnf, Docker, prebuilt archives, or go install."
weight: 20
---

kaku is a single static binary.
Every channel below ships the same build for the same tag.

## Homebrew (macOS and Linux)

```bash
brew install tamnd/tap/kaku
```

## Scoop (Windows)

```powershell
scoop bucket add tamnd https://github.com/tamnd/scoop-bucket
scoop install kaku
```

## Linux (apt and dnf)

A signed apt and dnf repository tracks every release, so `apt upgrade` and `dnf upgrade` keep kaku current.

```bash
# Debian, Ubuntu
curl -fsSL https://linux.tamnd.com/gpg.key \
  | sudo gpg --dearmor -o /usr/share/keyrings/tamnd.gpg
echo "deb [signed-by=/usr/share/keyrings/tamnd.gpg] https://linux.tamnd.com/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/tamnd.list
sudo apt update && sudo apt install kaku

# Fedora, RHEL
sudo dnf config-manager --add-repo https://linux.tamnd.com/dnf/tamnd.repo
sudo dnf install kaku
```

## Docker

```bash
docker run -it -v "$PWD:/work" -e ANTHROPIC_API_KEY ghcr.io/tamnd/kaku
```

The image carries bash, git, and ripgrep so the tools and checkpoints work inside the container.
Mount the project you want kaku to work on at `/work`.

## Prebuilt archives

Every release on the [releases page](https://github.com/tamnd/kaku/releases) has tar.gz and zip archives for Linux, macOS, Windows, and FreeBSD on amd64 and arm64 (plus 386 and armv7 for Linux), with SBOMs and a cosign-signed checksum file.

```bash
cosign verify-blob \
  --certificate checksums.txt.pem --signature checksums.txt.sig \
  --certificate-identity-regexp 'github.com/tamnd/kaku' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

## go install

```bash
go install github.com/tamnd/kaku/cmd/kaku@latest
```

## Verify

```bash
kaku --version
```

Then set a key and go:

```bash
export ANTHROPIC_API_KEY=...
kaku
```
