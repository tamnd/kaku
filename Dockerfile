# Consumed by GoReleaser: it copies the already cross-compiled binary out of the
# build context rather than compiling, so the image build is fast and uses the
# same static binary every other artifact ships.
#
# kaku is a pure-Go agent, but its tools shell out: bash for the bash tool, git
# for checkpoints, and ripgrep for fast search. Mount your project at /work.
#
# GoReleaser builds one multi-platform image with buildx and stages each
# platform's binary under a $TARGETPLATFORM directory (e.g. linux/amd64/) in the
# build context, so the COPY line selects the right one through the automatic
# TARGETPLATFORM build arg.
FROM alpine:3.21

ARG TARGETPLATFORM

RUN apk add --no-cache ca-certificates tzdata bash git ripgrep \
 && adduser -D -u 10001 kaku \
 && mkdir -p /work \
 && chown kaku:kaku /work

COPY $TARGETPLATFORM/kaku /usr/bin/kaku

USER kaku
WORKDIR /work

# Run against a mounted project:
#
#   docker run -it -v "$PWD:/work" -e ANTHROPIC_API_KEY ghcr.io/tamnd/kaku
#
VOLUME ["/work"]

ENTRYPOINT ["/usr/bin/kaku"]
