#!/bin/sh
set -e

NAUTROUDS_SERVICES_DIR=${NAUTROUDS_SERVICES_DIR:-/var/run/nautrouds/services}
NAUTROUDS_ENTRYPOINT_DIR=${NAUTROUDS_ENTRYPOINT_DIR:-/var/run/nautrouds/entrypoints}
NAUTROUDS_SERVICES_DIR_MODE=${NAUTROUDS_SERVICES_DIR_MODE:-1777}
NAUTROUDS_ENTRYPOINT_DIR_MODE=${NAUTROUDS_ENTRYPOINT_DIR_MODE:-0755}

if [ -n "$NAUTROUDS_UID" ] || [ -n "$NAUTROUDS_GID" ]; then
    TARGET_UID=${NAUTROUDS_UID:-$(id -u nautrouds)}
    TARGET_GID=${NAUTROUDS_GID:-$(id -g nautrouds)}
    CURRENT_UID=$(id -u nautrouds)
    CURRENT_GID=$(id -g nautrouds)

    if [ "$CURRENT_UID" != "$TARGET_UID" ] || [ "$CURRENT_GID" != "$TARGET_GID" ]; then
        deluser nautrouds
        delgroup nautrouds
        addgroup -S -g "$TARGET_GID" nautrouds
        adduser -S -G nautrouds -u "$TARGET_UID" nautrouds
    fi
fi

mkdir -p "$NAUTROUDS_SERVICES_DIR" "$NAUTROUDS_ENTRYPOINT_DIR"

chown -R nautrouds:nautrouds /var/run/nautrouds

chown nautrouds:nautrouds "$NAUTROUDS_SERVICES_DIR"
chmod "$NAUTROUDS_SERVICES_DIR_MODE" "$NAUTROUDS_SERVICES_DIR"
chmod "$NAUTROUDS_ENTRYPOINT_DIR_MODE" "$NAUTROUDS_ENTRYPOINT_DIR"

other_digit=$(printf '%s' "$NAUTROUDS_SERVICES_DIR_MODE" | sed 's/.*\(.\)$/\1/')
case "$other_digit" in
    2|3|6|7) other_writable=1 ;;
    *) other_writable=0 ;;
esac

if command -v setfacl >/dev/null 2>&1; then
    setfacl -b "$NAUTROUDS_SERVICES_DIR"

    setfacl -m u:nautrouds:rwx "$NAUTROUDS_SERVICES_DIR"
    setfacl -d -m u:nautrouds:rwx "$NAUTROUDS_SERVICES_DIR"

    if [ "$other_writable" = "1" ]; then
        setfacl -m o::rwx "$NAUTROUDS_SERVICES_DIR"
        setfacl -d -m o::rwx "$NAUTROUDS_SERVICES_DIR"
    fi

    setfacl -R -m m::rwx "$NAUTROUDS_SERVICES_DIR"
    setfacl -R -d -m m::rwx "$NAUTROUDS_SERVICES_DIR"
fi

exec su-exec nautrouds /usr/local/bin/nautrouds "$@"
