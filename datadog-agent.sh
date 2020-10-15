#!/bin/bash

source .env.local
docker run --rm --name dogstatsd-client \
  -e DD_API_KEY -e DD_HOSTNAME=johnknutson -e DD_DOGSTATSD_NON_LOCAL_TRAFFIC=true \
  -p 8125:8125/udp datadog/dogstatsd:latest
