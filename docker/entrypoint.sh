#!/bin/sh
set -e

NAUTROUDS_SERVICES_DIR=${NAUTROUDS_SERVICES_DIR:-/var/run/nautrouds/services}
NAUTROUDS_ENTRYPOINT_DIR=${NAUTROUDS_ENTRYPOINT_DIR:-/var/run/nautrouds/entrypoints}

mkdir -p "$NAUTROUDS_SERVICES_DIR" "$NAUTROUDS_ENTRYPOINT_DIR"

chown -R nautrouds:nautrouds /var/run/nautrouds

chown nautrouds:nautrouds "$NAUTROUDS_SERVICES_DIR"
chmod 1777 "$NAUTROUDS_SERVICES_DIR"
chmod 0755 "$NAUTROUDS_ENTRYPOINT_DIR"

if command -v setfacl >/dev/null 2>&1; then
    setfacl -b "$NAUTROUDS_SERVICES_DIR"

    setfacl -m u:nautrouds:rwx "$NAUTROUDS_SERVICES_DIR"
    setfacl -d -m u:nautrouds:rwx "$NAUTROUDS_SERVICES_DIR"

    setfacl -m o::rwx "$NAUTROUDS_SERVICES_DIR"
    setfacl -d -m o::rwx "$NAUTROUDS_SERVICES_DIR"

    setfacl -R -m m::rwx "$NAUTROUDS_SERVICES_DIR"
    setfacl -R -d -m m::rwx "$NAUTROUDS_SERVICES_DIR"
fi

exec su-exec nautrouds /usr/local/bin/nautrouds "$@"
