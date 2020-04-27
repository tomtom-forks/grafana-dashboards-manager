FROM ubuntu:18.04

RUN  apt-get -yq update && \
     apt-get -yqq install ssh

RUN ssh-keyscan github.com > /etc/ssh/ssh_known_hosts

RUN mkdir /etc/grafana-dashboards-manager/ && chown -R 65534:65534 /etc/grafana-dashboards-manager

ADD bin/puller /
ADD bin/pusher /
ADD run-pull-push.sh /run.sh

RUN chown 65534:65534 /puller && chown 65534:65534 /pusher && chown 65534:65534 /run.sh && chmod +x /run.sh

CMD ["/run.sh"]
