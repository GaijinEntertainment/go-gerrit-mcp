# Runtime image for the release pipeline: goreleaser (dockers_v2) lays the
# prebuilt binaries out per platform in the build context. distroless/static
# ships CA certificates (required for HTTPS to Gerrit) and runs as a non-root
# user.
FROM gcr.io/distroless/static-debian12:nonroot

ARG TARGETPLATFORM

COPY $TARGETPLATFORM/go-gerrit-mcp /usr/local/bin/go-gerrit-mcp

ENTRYPOINT ["/usr/local/bin/go-gerrit-mcp"]
