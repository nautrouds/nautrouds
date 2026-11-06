# Ntufile Syntax

An `Ntufile` defines routes, middleware, and virtual services.

## Rules

```text
[METHOD] [URL] <SERVICE>
    $Middleware("args")
    @tag
```

A rule has 1–3 columns, followed by indented middleware and tags:

| Columns | Format | METHOD | URL |
| :--- | :--- | :--- | :--- |
| 1 | `service` | `*` | `*/[|*]` |
| 2 | `url service` | `*` | `url` |
| 3 | `method url service` | `method` | `url` |

```text
# Catch-all
backend-default

# Any method
/api/* api-service

# Specific method
POST /upload/* storage-service
    $IPAllow("192.168.0.0/16")
```

### Block Syntax

Middleware and tags can be wrapped in `{ }` instead of being indented. These are equivalent:

```text
POST /upload/* storage-service
    $IPAllow("192.168.0.0/16")
    @no-metrics

POST /upload/* storage-service {
    $IPAllow("192.168.0.0/16")
    @no-metrics
}

POST /upload/* storage-service{$IPAllow("192.168.0.0/16");@no-metrics}
```

- `{` can end the rule line or open the next line.
- Indentation inside `{ }` is optional.
- `}` must close before the next rule.
- `;` is required between two bare (unparenthesized) directives, otherwise they merge into one invalid value; it's optional after `)`.

### Comments

- `#`: single-line comment.
- `#*`: block comment, until the next blank line.

### Tags

A line starting with `@` attaches metadata to the rule.

| Tag | Description |
| :--- | :--- |
| `@no-metrics` / `@!metrics` | Excludes the route from per-request HTTP metrics (`nautrouds_http_requests_total`, byte counters, `nautrouds_request_duration_seconds`, `nautrouds_own_duration_seconds`, etc.). |

---

## Built-in Middleware

| Name | Arguments | Description |
| :--- | :--- | :--- |
| `$SetHeader` | `(key, value)` | [Set response header](#headers) |
| `$DelHeader` | `(key)` | [Delete header](#headers) |
| `$SetHost` | `(host)` | [Overwrite `Host`](#headers) |
| `$PathTrimPrefix` | `(prefix)` | [Trim path prefix](#path--query) |
| `$RewritePath` | `(old, new)` | [Rewrite path](#path--query) |
| `$SetQuery` | `(key, value)` | [Set query parameter](#path--query) |
| `$BasicAuth` | `(user, pass)` | [Basic authentication](#access-control) |
| `$RequireHeader` | `(key, value)` | [Require header value](#access-control) |
| `$IPAllow` | `(cidr)` / `(headerKey, cidr)` | [Restrict by CIDR](#access-control) |
| `$BodySizeLimit` | `(size)` | [Limit body size](#limits) |
| `$RetryLimit` | `(count)` | [Limit dial retries](#limits) |
| `$Log` | `(line)` | [Print a log line](#logging) |

### Headers

- `$SetHeader`: sets a response header.
- `$DelHeader`: deletes a header.
- `$SetHost`: overwrites the `Host` header.

### Path & Query

- `$PathTrimPrefix`: removes a prefix from the path.
- `$RewritePath`: replaces a pattern in the path.
- `$SetQuery`: sets a query parameter.

### Access Control

- `$BasicAuth`: basic authentication.
- `$RequireHeader`: returns `403` unless the header equals `value`.
- `$IPAllow`: restricts by CIDR.
  - With one argument, checks the rightmost `X-Forwarded-For` entry; with `headerKey`, checks that header instead.
  - See [Security Considerations](../README.md#2-ipallow-header-mode) before using the header form.

### Limits

- `$BodySizeLimit`: returns `413` if the body exceeds `size`.
  - Accepts bytes or a 1024-based `KB`/`MB`/`GB` suffix, e.g. `512`, `10KB`, `5MB`, `1GB`.
  - Must be the last middleware; checked at compile time.
- `$RetryLimit`: caps how many more nodes are tried after a dial failure.
  - Only `GET`/`HEAD`/`OPTIONS`/`TRACE` retry; without a limit, they try every node.
  - Nodes known to be down are always skipped.

### Logging

- `$Log`: prints `line` to stdout.
  - Supports `{method}`, `{path}`, `{remoteip}` (rightmost `X-Forwarded-For`), `{header.X}`.

## External Middleware

A name without `$` forwards to the UDS service of that name:

```text
auth-service("/check", "header=X-User-Id", "header=X-User-Role")
```

| Argument | Meaning |
| :--- | :--- |
| `args[0]` | [Service path](#arguments) |
| `header=<name>` | [Copy response header](#arguments) |

### Arguments

- `args[0]`: path called on the service (default `/`).
- `header=<name>`: when the service approves (204), copies this response header onto the request.
  - Other response headers and arguments without this prefix are ignored.

## mmfg Middleware

`$mmfg("service/node")` forwards to a node registered via [mmfg-http](https://github.com/nautrouds/mmfg-http) instead of HTTP-over-UDS.

```text
GET /api/* backend-service
    $mmfg("mmfg-service/auth")
```

- Unix only; elsewhere the route returns `500`.
- The node socket must be `unixpacket`, named `<node>.mmfg`.
- An optional `<node>.ctl.mmfg` control socket (also `unixpacket`) enables self-respond.

---

## Virtual Services

Endpoints served by the core itself, used in place of a service name.

| Name | Arguments | Description |
| :--- | :--- | :--- |
| `$echo` | - | Request info as JSON. |
| `$ok` | `(msg?)` | `200` with optional body. |
| `$health` | - | Same as `$ok`. |
| `$err` | `(code, msg?)` | Custom status and message. |
| `$json` | `(body?)` | Custom JSON response. |
| `$redirect` | `(code, url)` | Redirect. |
| `$welcome` | - | HTML welcome page with a quick-start. |
| `$metrics` | - | Prometheus metrics. |
| `$ping` | `(service?)` | Checks connectivity to a backend service. |
| `$services` | - | Active services and nodes as JSON. |

---

## Routing

- Paths are matched segment by segment on a radix tree.
- `*` matches any segment, with backtracking.
- Specific paths take priority over wildcards.
- A path that matches but with the wrong method returns `405` or `404`, depending on the tree.
