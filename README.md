# F35

F35 is an end-to-end DNS resolver scanner for real tunnel testing.

It does not only ask a DNS question and call that a success.
It actually:

1. starts a tunnel client
2. uses one resolver from your list
3. waits for the tunnel to become usable
4. sends a real HTTP request through the tunnel
5. prints only resolvers that really pass traffic

This is useful when you want to find resolvers that still have outside connectivity during heavy filtering or shutdown conditions.

## What Is A Resolver Here?

A resolver is the DNS server IP you want to test.

Examples:

```txt
1.1.1.1
8.8.8.8:53
10.10.34.1
```

If you give only an IP, F35 uses port `53` automatically.

## What You Need Before Running

You need all of these:

- a file with resolver IPs
- a working tunnel domain
- one tunnel client:
  - `dnstt-client`
  - `slipstream-client`
  - `vaydns-client`
- the extra flags that your tunnel client needs, passed with `-a`

## Build

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o f35 .
```

## Flags

- `-r`
  file that contains resolver IPs
- `-e`
  which tunnel client to use: `dnstt`, `slipstream`, or `vaydns`
- `-d`
  tunnel domain
- `-a`
  extra flags for your tunnel client, for example pubkey, timeouts, log level, or custom tuning
- `-x`
  proxy protocol F35 uses for the request through the tunnel
  this must match what your tunnel path or server-side target expects
  wrong `-x` can make healthy resolvers look dead
  default is `socks5h`
- `-U`
  proxy username if the tunnel exit requires authentication
- `-P`
  proxy password if the tunnel exit requires authentication
  `-P` requires `-U`
- `-u`
  test URL used for the real HTTP request through the tunnel
  default is `http://www.google.com/gen_204`
- `-w`
  how many resolvers to test at the same time
- `-s`
  how long to wait before the HTTP test, in milliseconds
  this is important because the tunnel may need time to become usable
- `-t`
  HTTP request timeout in seconds
- `-R`
  retry count for each resolver
- `-l`
  starting local port for local tunnel listeners
  useful if you want to avoid port collisions or run multiple scans
- `-p`
  full path to the tunnel client binary if it is not in `PATH`
- `-whois`
  after a resolver works, also print resolver organization and country
- `-json`
  print one JSON object per result line instead of plain text

## How `-a` Works

`-a` is only for tunnel client flags.

Examples:

- DNSTT:
  `-a '-pubkey YOUR_PUBLIC_KEY'`
- VayDNS:
  `-a '-pubkey YOUR_PUBLIC_KEY -log-level info -udp-timeout 200ms'`

F35 automatically fills these parts for you:

- resolver address
- local listen address
- domain

For `dnstt`, F35 places `-a` before the positional `domain` and `listen` arguments.

## First Real Example

If you are new, start with something like this:

```bash
f35 -r resolvers.txt -e dnstt -d t.example.com -x socks5h -a '-pubkey YOUR_PUBLIC_KEY'
```

What this means:

- read resolvers from `resolvers.txt`
- use `dnstt-client`
- connect to `t.example.com`
- send the HTTP test through the tunnel using the `socks5h` protocol
- pass the public key to the client

## More Examples

### DNSTT

```bash
f35 -r resolvers.txt \
  -e dnstt \
  -d t.example.com \
  -x socks5h \
  -a '-pubkey YOUR_PUBLIC_KEY'
```

### VayDNS

```bash
f35 -r resolvers.txt \
  -e vaydns \
  -d t.example.com \
  -x socks5h \
  -a '-pubkey YOUR_PUBLIC_KEY -log-level info -udp-timeout 200ms -open-stream-timeout 7s -idle-timeout 10s -keepalive 2s -udp-workers 200 -rps 300 -max-streams 0 -max-qname-len 101 -max-num-labels 2'
```

### Slipstream

```bash
f35 -r resolvers.txt \
  -e slipstream \
  -d t.example.com \
  -x socks5h
```

### Proxy Auth With `-U` And `-P`

Use this if the proxy exposed by your tunnel requires a username and password:

```bash
f35 -r resolvers.txt \
  -e dnstt \
  -d t.example.com \
  -x socks5h \
  -U myuser \
  -P mypass \
  -a '-pubkey YOUR_PUBLIC_KEY'
```

`-P` only works together with `-U`.

### Save Only Healthy Resolvers

```bash
f35 -r resolvers.txt -e dnstt -d t.example.com -x socks5h -a '-pubkey YOUR_PUBLIC_KEY' | tee healthy.txt
```

### Use A Binary That Is Not In PATH

```bash
f35 -r resolvers.txt -e vaydns -d t.example.com -x socks5h -p ./vaydns-client -a '-pubkey YOUR_PUBLIC_KEY'
```

### Make The Scan More Conservative

This is useful when resolvers are slow but still usable.

```bash
f35 -r resolvers.txt -e vaydns -d t.example.com -x socks5h -w 50 -s 2000 -t 8 -R 2 -a '-pubkey YOUR_PUBLIC_KEY'
```

Meaning:

- fewer concurrent workers
- longer tunnel warm-up wait
- longer HTTP timeout
- retry failed resolvers

### Show Resolver Owner Info

```bash
f35 -r resolvers.txt -e vaydns -d t.example.com -x socks5h -whois -a '-pubkey YOUR_PUBLIC_KEY'
```

This keeps the normal health test, and if a resolver works, it also prints org and country for that resolver IP.

This is most useful when the resolver IP itself belongs to the network you care about.
If your tunnel goes into a more advanced upstream chain, this extra lookup can be less meaningful.

### JSON Output

```bash
f35 -r resolvers.txt -e vaydns -d t.example.com -x socks5h -whois -json -a '-pubkey YOUR_PUBLIC_KEY'
```

Use this if you want to parse the output in another program.

## Important Note About Advanced Upstreams

F35 does not generate advanced proxy protocol packets by itself.
It only sends a normal HTTP request through the tunnel using the protocol selected with `-x`.

Examples:

- if your tunnel path expects SOCKS, use `-x socks5` or `-x socks5h`
- if your tunnel path expects HTTP proxy traffic, use `-x http`

If you use something more advanced behind the tunnel, like `vless+ws`, F35 is not generating native `vless+ws` traffic.
It is only checking whether the tunnel path can move a request and return any response.

That means:

- the first request is still enough to decide healthy or dead
- F35 does not require HTTP `200`
- even `400` or `404` can still prove that the tunnel is working
- `-whois` may be less useful in those advanced chains
- wrong `-x` can ruin scan results

## Output

### Normal Output

```txt
1.2.3.4:53 342ms
5.6.7.8:53 89ms
```

Only healthy resolvers are printed.

A resolver is considered healthy if the first request really moves through the tunnel and any response comes back.
F35 does not require HTTP `200`.
Even a `400` or `404` can still prove that the tunnel is working.

Latency is colored on terminal output:

- green: `0-2000ms`
- yellow: `2000-6000ms`
- red: `6000ms+`

If you pipe the output to a file or another command, colors are not printed.

### Output With `-whois`

```txt
1.2.3.4:53 342ms Iran Information Technology Company PJSC Iran
5.6.7.8:53 2140ms unknown unknown
```

### Output With `-json`

```json
{"resolver":"1.2.3.4:53","latency_ms":342}
{"resolver":"5.6.7.8:53","latency_ms":2140,"org":"Iran Information Technology Company PJSC","country":"Iran"}
```

## Good Defaults For New Users

If you do not know what to tune first, try this order:

1. keep `-x socks5h`
2. if output is empty, increase `-s`
3. if working resolvers are slow, increase `-t`
4. if results are unstable, lower `-w`
5. if some resolvers fail randomly, add `-R 1` or `-R 2`

## Troubleshooting

### `binary ... not found in PATH`

The selected tunnel client binary was not found.

Fix it with one of these:

- install the client
- add it to `PATH`
- use `-p /full/path/to/client`

### No Output

Usually one of these is wrong:

- domain
- engine
- pubkey or other tunnel client flags inside `-a`
- wait time is too short
- timeout is too short

Try this:

```bash
-s 2000 -t 8 -R 1
```

### `-P requires -U`

If you set a proxy password, you must also set a proxy username.

### Very Few Working Resolvers

Try:

- lower `-w`
- increase `-s`
- increase `-t`
- add retries with `-R`

### I Do Not Know What To Put In `-a`

Put the same client flags you normally use when running your tunnel client manually.

F35 is not replacing your tunnel client config.
It is only fuzzing resolvers and local listen ports around that client command.
