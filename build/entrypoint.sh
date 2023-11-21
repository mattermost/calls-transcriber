#!/bin/sh

chown -R calls:calls /data
exec runuser -u calls "$@"
