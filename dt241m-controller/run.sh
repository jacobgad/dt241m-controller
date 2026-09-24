#!/usr/bin/with-contenv bashio
set -euo pipefail

if ! bashio::services.available "mqtt"; then
  bashio::exit.nok "No MQTT service is available. Install and start the Mosquitto broker add-on, then restart this add-on."
fi

MQTT_HOST="$(bashio::services mqtt "host")"
MQTT_PORT="$(bashio::services mqtt "port")"
MQTT_USERNAME="$(bashio::services mqtt "username")"
MQTT_PASSWORD="$(bashio::services mqtt "password")"
MQTT_SSL="$(bashio::services mqtt "ssl")"

export MQTT_HOST MQTT_PORT MQTT_USERNAME MQTT_PASSWORD MQTT_SSL
export DT241M_OPTIONS_PATH="/data/options.json"
export DT241M_DATABASE_PATH="/data/dt241m.sqlite"

bashio::log.info "Starting DT241M Controller (MQTT broker ${MQTT_HOST}:${MQTT_PORT})"
exec node /app/dist/index.js
