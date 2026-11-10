ARG JDK_BASE_IMAGE=mcr.microsoft.com/openjdk/jdk:21-ubuntu
FROM ${JDK_BASE_IMAGE}
USER root
COPY vendor/apache-jmeter-5.6.3.tgz vendor/apache-jmeter-5.6.3.tgz.sha512 /tmp/
RUN cd /tmp && sha512sum -c apache-jmeter-5.6.3.tgz.sha512 \
    && tar -xzf apache-jmeter-5.6.3.tgz -C /opt \
    && rm apache-jmeter-5.6.3.tgz apache-jmeter-5.6.3.tgz.sha512 \
    && groupadd --gid 10001 jmeter \
    && useradd --uid 10001 --gid 10001 --create-home jmeter \
    && mkdir -p /work && chown 10001:10001 /work
ENV JMETER_HOME=/opt/apache-jmeter-5.6.3
ENV PATH=/opt/apache-jmeter-5.6.3/bin:${PATH}
COPY bin/entrypoint.sh /opt/cluster/bin/entrypoint.sh
COPY conf /opt/cluster/conf
COPY vendor/jmeter-executor-linux-amd64 /opt/cluster/bin/jmeter-executor
COPY vendor/pressurecooker-thread-observer.jar /opt/apache-jmeter-5.6.3/lib/ext/pressurecooker-thread-observer.jar
RUN chmod 755 /opt/cluster/bin/jmeter-executor
RUN chmod 755 /opt/cluster/bin/entrypoint.sh
WORKDIR /work
USER 10001:10001
ENTRYPOINT ["/opt/cluster/bin/entrypoint.sh"]
CMD ["agent"]
