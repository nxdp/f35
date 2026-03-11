# F35

**F35** is an end-to-end DNS resolver scanner for real tunnel testing with **dnstt** or **slipstream**.

It establishes real tunnels, sends HTTP traffic through them, and measures latency. It does not just check DNS replies.

---

## Quick Start

1. Build:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o f35 .
```

2. Create `resolvers.txt`:

```txt
1.1.1.1
8.8.8.8:53
9.9.9.9
```

3. Run a basic scan:

```bash
f35 -r resolvers.txt -e dnstt -k YOUR_PUBLIC_KEY -d t.example.com -x socks5h
```

4. Save results:

```bash
f35 -r resolvers.txt -e dnstt -k YOUR_PUBLIC_KEY -d t.example.com -x socks5h | tee healthy.txt
```

Windows example (no PATH change needed):

```bash
f35.exe -r resolvers.txt -e dnstt -k YOUR_PUBLIC_KEY -d t.example.com -x socks5h -p .\\dnstt-client.exe
```

---

## What F35 Does

F35 takes a resolver list (`IP` or `IP:PORT`), a tunnel domain, and worker count.

For each resolver, F35:

1. Spawns either `dnstt-client` or `slipstream-client` on a local port.
2. Waits for tunnel establishment (`-s`, milliseconds).
3. Sends an HTTP request through the tunnel.
4. If a response is received within the timeout, the resolver is **healthy** — its address and latency are printed to stdout.
5. If the request times out or fails, the resolver is silently skipped.
6. Stops the tunnel client process and moves on.

This makes F35 a true end-to-end resolver scanner, not a synthetic test.

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

* A tunnel client in `$PATH`:
  * `dnstt-client` when using `-e dnstt`
  * `slipstream-client` when using `-e slipstream`
  * or provide an explicit path via `-p`
* A reachable tunnel server for the selected engine
* A valid tunnel domain (e.g. `t.example.com`)
* For DNSTT: server public key via `-k`

---

## Usage

```
  -P string
        Optional proxy password
  -U string
        Optional proxy username
  -d string
        Tunnel domain (e.g. t.example.com)
  -e string
        Tunnel engine: dnstt|slipstream (default "dnstt")
  -k string
        DNSTT public key (required when -e dnstt)
  -l int
        Starting local port for tunnel listeners (default 40000)
  -p string
        Explicit path to client binary (optional)
  -r string
        Path to resolvers file
  -R int
        Retries per resolver after first failed attempt (default 0)
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

Examples:

```bash
f35 -r resolvers.txt \
	-e dnstt \
	-k YOUR_PUBLIC_KEY \
	-d t.example.com \
	-s 3000 \
	-t 5 \
	-w 100 \
	-x socks5h \
	-U user -P pass \
	-u http://www.google.com/gen_204
```

```bash
f35 -r resolvers.txt -e dnstt -k YOUR_PUBLIC_KEY -d t.example.com -w 100 -x socks5h

f35 -r resolvers.txt -e dnstt -k YOUR_PUBLIC_KEY -d t.example.com -w 100 -x socks5h -s 1500

f35 -r resolvers.txt -e slipstream -d t.example.com -w 100 -x http

f35 -r resolvers.txt -e slipstream -d t.example.com -w 100 -x socks5h

f35 -r resolvers.txt -e slipstream -d t.example.com -w 100 -x http -U user -P pass

f35 -r resolvers.txt -e dnstt -k YOUR_PUBLIC_KEY -d t.example.com -w 100 -x socks5h -R 2
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
f35 -r resolvers.txt -e dnstt -k YOUR_PUBLIC_KEY -d t.example.com | tee results.txt
```

---

## Troubleshooting

- `error: dnstt-client not found in PATH` or `error: slipstream-client not found in PATH`:
  install the selected engine client, add it to `PATH`, or use `-p` to set a full path.
- `error: -k is required when -e dnstt`:
  pass the DNSTT public key with `-k`.
- Empty output:
  check domain, engine selection (`-e`), key (`-k` for DNSTT), proxy mode (`-x`), auth (`-U/-P`), and try a larger wait like `-s 2000`.
- Very few results:
  lower concurrency (`-w`) or increase timeout (`-t`), wait time (`-s`), or retries (`-R`).
