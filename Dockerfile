# GoReleaser builds the static binary and copies it in; this image just wraps it.
# Fonts are embedded in the binary, so no runtime assets are needed.
FROM gcr.io/distroless/static:nonroot
COPY inkcap /inkcap
WORKDIR /work
ENTRYPOINT ["/inkcap"]
