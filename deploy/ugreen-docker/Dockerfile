FROM scratch

COPY rootfs/ /

ENV PATH=/usr/local/bin:/usr/bin:/bin
ENV TZ=Asia/Shanghai

EXPOSE 9867
ENTRYPOINT ["/usr/local/bin/panzhan"]
CMD ["-listen", "0.0.0.0:9867", "-data", "/data", "-root", "/downloads"]
