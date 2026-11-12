FROM scratch
ARG VERSION
ARG COMMIT
LABEL org.opencontainers.image.title="AEL Server" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$COMMIT
COPY --chown=65532:65532 ael /ael
COPY THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
COPY --chown=65532:65532 data /data
USER 65532:65532
WORKDIR /
EXPOSE 7332
VOLUME ["/data"]
ENTRYPOINT ["/ael"]
CMD ["server", "start", "--data-dir", "/data", "--addr", "0.0.0.0:7332"]
