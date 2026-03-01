# F35

**F35** is an end‑to‑end DNS resolver scanner built to test real-world behavior of resolvers when used with **slipstream** tunnels.

It evaluates resolvers by actually establishing tunnels, sending traffic through them, and measuring real HTTP latency — not just whether the resolver replies.

---

## What F35 Does

F35 takes a list of DNS resolvers (`IP:PORT`), a tunnel domain pointing to a production **slipstream-server**, and a worker count.

For each resolver, F35:

1. Spawns a `slipstream-client` process bound to a local port.
2. Sends an HTTP request immediately through the tunnel.
3. If a response is received within the timeout, the resolver is **healthy** — its address and latency are printed to stdout.
4. If the request times out or fails, the resolver is silently skipped.
5. Kills the `slipstream-client` process and moves on.

This makes F35 a **true end-to-end resolver scanner**, not a synthetic or partial test.

---

## Resolver List Format

One `IP:PORT` per line:

```
1.1.1.1:53
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
  -domain string
        Tunnel domain (e.g. ns.domain.tld)
  -resolvers string
        Path to resolvers file
  -start-port int
        Starting local port for tunnel listeners (default 40000)
  -test-url string
        HTTP URL to test through tunnel (default "http://www.google.com/gen_204")
  -timeout int
        HTTP request timeout in seconds (default 5)
  -workers int
        Concurrent workers (default 20)
```

Example:

```bash
./f35 -resolvers resolvers.txt -domain ns.domain.tld -workers 100
```

---

## Output

Healthy resolvers are printed to **stdout** as they are discovered, one per line:

```
1.2.3.4:53 342ms
5.6.7.8:5353 89ms
```

Unhealthy resolvers produce no output.

Pipe stdout directly into another tool or file:

```bash
./f35 -resolvers resolvers.txt -domain ns.domain.tld | tee results.txt
```
