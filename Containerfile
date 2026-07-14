# SPDX-License-Identifier: Apache-2.0

# The manifest-list digest is intentionally pinned so every supported Linux architecture resolves
# to the reviewed distroless static runtime, never a floating tag.
FROM gcr.io/distroless/static-debian12@sha256:b7bb25d9f7c31d2bdd1982feb4dafcaf137703c7075dbe2febb41c24212b946f

ARG TARGETARCH

# The build context contains only a static Linux binary assembled by the test or release tooling.
COPY --chown=65532:65532 --chmod=0555 bin/linux/${TARGETARCH}/sith /usr/local/bin/sith

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/sith"]
