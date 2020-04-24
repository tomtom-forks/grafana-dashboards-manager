#!/usr/bin/env sh

echo "$GIT_DEPLOY_KEY" > /etc/grafana-dashboards-manager/grafana-dashboards-deploy-key
# replace Grafana API key in config.yaml here

sed -i "s/grafana_api_key/$GRAFANA_API_KEY/g" /etc/grafana-dashboards-manager/config.yaml

./puller -config='/etc/grafana-dashboards-manager/config.yaml'

sleep 5

./pusher -config='/etc/grafana-dashboards-manager/config.yaml' -single-shot
