# F35

**F35** is an end‑to‑end DNS resolver scanner built to test real-world behavior of resolvers when used with **slipstream** tunnels.

It evaluates resolvers by actually establishing tunnels, sending traffic through them, and measuring real HTTP latency — not just whether the resolver replies.

---

## What F35 Does

F35 takes a list of DNS resolvers (`IP` or `IP:PORT`), a tunnel domain pointing to a production **slipstream-server**, and a worker count.

For each resolver, F35:

1. Spawns a `slipstream-client` process bound to a local port.
2. Waits for tunnel establishment (`-s`, milliseconds).
3. Sends an HTTP request through the tunnel.
4. If a response is received within the timeout, the resolver is **healthy** — its address and latency are printed to stdout.
5. If the request times out or fails, the resolver is silently skipped.
6. Kills the `slipstream-client` process and moves on.

This makes F35 a **true end-to-end resolver scanner**, not a synthetic or partial test.

---

## Resolver List Format

One resolver per line:
- `IP` (defaults to port `53`)
- `IP:PORT`

```
1.1.1.1
8.8.8.8:53
```

---

## Requirements

* **slipstream-client** must be installed and available in `$PATH`
* A running and reachable **slipstream-server**
* A valid tunnel domain (e.g. `ns.domain.tld`)

---

## Usage

```
  -P string
        Optional proxy password
  -U string
        Optional proxy username
  -d string
        Tunnel domain (e.g. ns.domain.tld)
  -l int
        Starting local port for tunnel listeners (default 40000)
  -r string
        Path to resolvers file
  -s int
        Milliseconds to wait for tunnel establishment before HTTP test (default 1000)
  -t int
        HTTP request timeout in seconds (default 5)
  -u string
        HTTP URL to test through tunnel (default "http://www.google.com/gen_204")
  -w int
        Concurrent workers (default 20)
  -x string
        Proxy protocol for listener: http|https|socks5|socks5h (default "http")
```

Example:

```bash
./f35 -r resolvers.txt -d ns.domain.tld -w 100 -x http
./f35 -r resolvers.txt -d ns.domain.tld -w 100 -x socks5
./f35 -r resolvers.txt -d ns.domain.tld -w 100 -x socks5h
./f35 -r resolvers.txt -d ns.domain.tld -w 100 -x http -U user -P pass
./f35 -r resolvers.txt -d ns.domain.tld -w 100 -x socks5 -s 1500
```

---

## Output

Healthy resolvers are printed to **stdout** as they are discovered, one per line:

```
1.2.3.4:53 342ms
5.6.7.8:53 89ms
```

Unhealthy resolvers produce no output.

Pipe stdout directly into another tool or file:

```bash
./f35 -r resolvers.txt -d ns.domain.tld | tee results.txt
```
