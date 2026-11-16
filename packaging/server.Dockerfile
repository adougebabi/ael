FROM scratch
ARG VERSION
ARG COMMIT
ARG SOURCE=https://github.com/adougebabi/ael
LABEL org.opencontainers.image.source=$SOURCE \
      org.opencontainers.image.title="AEL Server" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$COMMIT
COPY --chown=65532:65532 ael /ael
COPY THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
COPY --chown=65532:65532 --chmod=0700 data /data
COPY --chown=65532:65532 --chmod=0700 data /backup
USER 65532:65532
WORKDIR /
EXPOSE 7332
VOLUME ["/data"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/ael", "server", "health", "--url", "http://127.0.0.1:7332", "--json"]
ENTRYPOINT ["/ael"]
CMD ["server", "start", "--data-dir", "/data", "--addr", "0.0.0.0:7332"]
