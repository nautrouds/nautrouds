# Ntufile Syntax Specification

Nautrouds uses a Domain Specific Language (DSL) called `Ntufile` to define routing rules, middleware, and virtual services.

## Basic Syntax

Each rule consists of 1 to 3 columns, followed by optional indented middleware directives.

### Rule Format
```text
[METHOD] [URL] <SERVICE>
    $Middleware(args)
```

### Column Variations
| Columns | Format | Result |
| :--- | :--- | :--- |
| 1 | `service` | Method=`*`, URL=`*/[|*]`, Service=`service` |
| 2 | `url service` | Method=`*`, URL=`url`, Service=`service` |
| 3 | `method url service`| Method=`method`, URL=`url`, Service=`service` |

### Examples
```text
# Catch-all for a specific service
backend-default

# Match all methods for a path
/api/* api-service

# Strict matching
POST /upload/* storage-service
    $IPAllow(192.168.0.0/16)
```

### Comments
- `#`: Single-line comment.
- `#*`: Block comment (skips until the next blank line).

---

## Built-in Middlewares ($)

Middlewares are applied to a route via indentation.

| Name | Arguments | Description |
| :--- | :--- | :--- |
| `$SetHeader` | `(key, value)` | Sets a response header. |
| `$DelHeader` | `(key)` | Deletes a header. |
| `$SetHost` | `(host)` | Overwrites the `Host` header. |
| `$PathTrimPrefix`| `(prefix)` | Removes prefix from URL path. |
| `$RewritePath` | `(old, new)` | Replaces pattern in URL path. |
| `$SetQuery` | `(key, value)` | Sets a query parameter. |
| `$BasicAuth` | `(user, pass)` | Basic Authentication. |
| `$IPAllow` | `(cidr)` | Restricts access by CIDR. |
| `$Log` | `(prefix)` | Logs request info to stdout. |

---

## Virtual Services ($)

Virtual services are functional endpoints provided by the Nautrouds core.

| Name | Arguments | Description |
| :--- | :--- | :--- |
| `$echo` | - | Returns request info as JSON. |
| `$ok` | `(msg?)` | Returns 200 OK with optional body. |
| `$err` | `(code, msg?)` | Returns custom error code and message. |
| `$health` | - | Synonym for `$ok`. |
| `$metrics` | - | Exposes internal Prometheus metrics. |
| `$redirect` | `(code, url)` | Performs a redirect. |
| `$json` | `(body?)` | Returns custom JSON response. |
| `$ping` | `(service?)` | Checks connectivity to a specific backend service. |
| `$services`| - | Returns a JSON list of all active services and nodes. |

---

## Routing Logic

1. **Radix Tree Matching**: Nautrouds uses a Radix Tree for O(1) or O(log N) path matching.
2. **Wildcards**: `*` matches any segment. Backtracking is supported for complex patterns.
3. **Priority**: Specific paths are matched before wildcards.
4. **Method Filtering**: If a method is specified (e.g., `POST`), requests with other methods will result in a 405 or 404 depending on tree structure.
