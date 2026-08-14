# GYDS Chain production TODO

This checklist records the work still needed for production wallet support and
the deployment failure shown in the uploaded screenshot.

## Plan 6 — Implement and verify `net_enode`

### Evidence from the uploaded genesis-node screenshot

The command reached the local RPC server on port `8545`, but the node returned:

```json
{
  "jsonrpc": "2.0",
  "error": {
    "code": -32601,
    "message": "method net_enode not found"
  },
  "id": 1
}
```

This confirms that the RPC service is running. The immediate failure is an
unimplemented JSON-RPC method, not a `127.0.0.1` connectivity failure.

### Implementation checklist

- [x] Add `GYDS_P2P_ADVERTISE_HOST` to configuration. It must be the genesis
  server's public IP or DNS name, not `0.0.0.0` or `127.0.0.1`.
- [x] Add the advertised P2P port configuration, defaulting to
  `GYDS_P2P_PORT=30303`.
- [x] Extend the RPC/P2P interface so RPC can read the local node ID,
  advertised host, and P2P port.
- [x] Implement `net_enode` in the JSON-RPC dispatcher.
- [ ] Return a documented, non-empty result containing the local node identity
  and reachable P2P endpoint. Keep the response format stable for node
  operators and tooling.
- [x] Keep the P2P bind address on all interfaces (`:30303`, equivalent to
  `0.0.0.0:30303`) while advertising only the public address.
- [x] Add a useful error when `GYDS_P2P_ADVERTISE_HOST` is empty for a node
  that is expected to accept remote peers.
- [x] Implement `net_peerCount` using the live P2P peer count instead of the
  current hardcoded `0x0`.
- [x] Add retry/backoff for bootstrap peers and log each dial, handshake,
  rejection, and reconnect event.
- [ ] Verify TCP `30303` is open in both the server firewall and hosting
  provider security group.
- [x] Ensure every node has the same chain ID/genesis hash and a unique
  `<GYDS_DATA_DIR>/node.key`.

### Configuration examples

Genesis node:

```env
GYDS_NODE_MODE=genesis
GYDS_CHAIN_ID=198282
GYDS_RPC_HOST=0.0.0.0
GYDS_RPC_PORT=8545
GYDS_P2P_PORT=30303
GYDS_P2P_ADVERTISE_HOST=GENESIS_PUBLIC_IP_OR_DNS
```

Joining node:

```env
GYDS_NODE_MODE=sync
GYDS_CHAIN_ID=198282
GYDS_RPC_HOST=0.0.0.0
GYDS_RPC_PORT=8545
GYDS_P2P_PORT=30303
GYDS_BOOTSTRAP_NODES=GENESIS_PUBLIC_IP_OR_DNS:30303
GYDS_P2P_ADVERTISE_HOST=JOINING_NODE_PUBLIC_IP_OR_DNS
```

`GYDS_BOOTSTRAP_NODES` must contain a real public `host:port`. Do not use the
HTTPS RPC URL, `127.0.0.1`, or `0.0.0.0` for node-to-node peering.

### Verification after implementation

Run on the genesis node:

```bash
curl -sS http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"net_enode","params":[],"id":1}' | jq

curl -sS http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"net_peerCount","params":[],"id":2}' | jq

curl -sS http://127.0.0.1:8545/api/peers | jq
sudo ss -lntp | grep -E ':(30303|8545)\b'
```

Run from each joining node:

```bash
nc -vz GENESIS_PUBLIC_IP_OR_DNS 30303
curl -sS http://127.0.0.1:8545/api/peers | jq
sudo journalctl -u gyds-fullnode -n 200 --no-pager \
  | grep -Ei 'p2p|peer|bootstrap|dial|handshake|auth|reconnect'
```

Acceptance criteria:

- [x] The screenshot command returns a successful `net_enode` result when
  `GYDS_P2P_ADVERTISE_HOST` is configured.
- [ ] The result never advertises `127.0.0.1` or `0.0.0.0`.
- [x] `net_peerCount` reports authorized live peers and matches `/api/peers`.
- [ ] Genesis and joining nodes show each other as connected.
- [ ] Joining nodes synchronize to the genesis node's block height.
- [ ] A service restart reconnects without manually recreating node identity.

## Plan 7 — Fix P2P peering, wallet storage, PIN configuration, and ports

### Findings from the current implementation

- [x] Confirm every joining node runs `full`, `genesis`, `sync`, `boost`, or
  `lite` mode. The `rpc` mode intentionally has **no P2P**.
- [x] The Go P2P listener uses `:30303`, which listens on all interfaces
  (`0.0.0.0`) rather than only `127.0.0.1`.
- [x] The HTTP/RPC listener defaults to `GYDS_RPC_HOST=0.0.0.0`; preserve this
  behavior for externally reachable nodes.
- [x] Treat `0.0.0.0` as a bind address only. Do **not** advertise
  `0.0.0.0:30303` to peers. Bootstrap nodes must use the real public IP or DNS
  name of the genesis node, for example `203.0.113.10:30303`.
- [ ] Verify that the genesis node and joining nodes use the same chain ID
  (`198282`) and the same genesis hash.
- [ ] Verify that every node has a unique persisted `<dataDir>/node.key`.
  Never copy the genesis node's `node.key` to another server.
- [ ] Check that the joining node has `GYDS_BOOTSTRAP_NODES=<genesis-public-ip>:30303`
  and is not using the wallet/RPC URL as its bootstrap address.
- [ ] Open TCP and UDP `30303` on the genesis server and confirm the hosting
  provider's firewall/security group also allows it. The current Go transport
  uses TCP; UDP is still useful if discovery is added later.
- [ ] Test the path from each joining node to the genesis node:

  ```bash
  nc -vz GENESIS_PUBLIC_IP 30303
  curl -sS http://127.0.0.1:8545/api/peers | jq
  journalctl -u gyds-fullnode -n 200 --no-pager | grep -Ei 'p2p|peer|bootstrap|dial|handshake|auth'
  ```

### Required P2P code fixes

- [x] Add a configurable advertised P2P host, for example
  `GYDS_P2P_ADVERTISE_HOST`, separate from the listener bind address. The
  advertised endpoint must be `public-host:30303`, never `0.0.0.0:30303`.
- [x] Add a stable `net_enode` or equivalent RPC response containing this
  node's public node ID and advertised P2P endpoint. The current
  `net_enode` request returns no useful result because it is not implemented in
  the RPC dispatcher.
- [x] Add `net_peerCount` from the actual P2P server. It now excludes pending
  and unauthorized connections.
- [x] Add a retry loop with backoff for `GYDS_BOOTSTRAP_NODES`.
- [ ] Start the P2P listener before outbound bootstrap dialing, then perform
  dialing asynchronously after the listener is ready.
- [ ] Log the configured bootstrap address, resolved address, dial error, local
  node ID, remote node ID, chain ID mismatch, and successful handshake.
- [ ] Reject or clearly report a peer when chain IDs or genesis hashes differ.
- [ ] Ensure P2P peers are removed from the peer map on every disconnect and
  that `/api/peers` reports only live, authorized connections.
- [ ] Add tests for inbound connection, outbound connection, retry behavior,
  duplicate node keys, chain mismatch, peer authorization, and disconnect
  cleanup.

### Port matrix and node-linking guide

Ports may be reused on different servers. They must be different when more
than one GYDS process runs on the same server.

| Listener | Default | Modes | Purpose |
|---|---:|---|---|
| Dashboard HTTP | 5000 | all modes except an intentionally disabled deployment | Browser dashboard, setup, guides, REST APIs |
| JSON-RPC HTTP | 8545 | all modes with RPC enabled | MetaMask, ethers.js, wallet RPC |
| WebSocket path | 8545 `/api/ws` | all modes with RPC enabled | WebSocket subscriptions; `GYDS_WS_PORT` is legacy compatibility only |
| P2P TCP | 30303 | full, lite, sync, boost, genesis, validator | Peer handshakes, blocks, transactions |
| P2P UDP | none currently | no mode | Reserved for future discovery; opening UDP is optional |
| `genesis` command | no listener | command only | Prints the canonical genesis JSON and exits |
| `rpc` mode P2P | none | rpc, testnode | RPC-only and isolated test nodes do not join the peer network |

For two nodes on one server, use a unique set such as dashboard `5000/5001`,
RPC `8545/8547`, and P2P `30303/30304`. On separate servers, both nodes can
use the defaults. The joining node's `GYDS_BOOTSTRAP_NODES` must point to the
genesis node's public P2P address, not its RPC or dashboard URL.

Genesis node:

```env
GYDS_NODE_MODE=genesis
GYDS_CHAIN_ID=198282
GYDS_DASHBOARD_PORT=5000
GYDS_RPC_PORT=8545
GYDS_P2P_PORT=30303
GYDS_P2P_ADVERTISE_HOST=genesis.example.com
GYDS_BOOTSTRAP_NODES=
```

Joining full, validator, boost, or lite node:

```env
GYDS_NODE_MODE=full        # or validator, boost, lite, or sync
GYDS_CHAIN_ID=198282
GYDS_DASHBOARD_PORT=5000
GYDS_RPC_PORT=8545
GYDS_P2P_PORT=30303
GYDS_P2P_ADVERTISE_HOST=joining.example.com
GYDS_BOOTSTRAP_NODES=genesis.example.com:30303
```

Linking procedure:

1. Build every node from the same repository revision and confirm the same
   chain ID/genesis output with `./bin/gyds-fullnode genesis`.
2. Run the genesis node first and share its `host:30303` endpoint.
3. Set `GYDS_BOOTSTRAP_NODES` on each joining node, open TCP `30303` in both
   the host firewall and cloud security group, then restart the joining node.
4. Verify `net_enode`, `net_peerCount`, `/api/peers`, and
   `eth_blockNumber` on both nodes. A node key is generated under each node's
   own `GYDS_DATA_DIR`; never copy `node.key` between nodes.
5. For public wallet use, publish HTTPS for the RPC origin and use the returned
   `/gyds-network.json` metadata. The stable `/logo.png` endpoint is available
   on both the dashboard and dedicated RPC origins.

The Replit preview proxy is suitable for the dashboard/RPC HTTP ports. Public
P2P joining requires a deployment or host that exposes TCP `30303` directly;
an HTTP preview URL cannot be used as a bootstrap peer.

### Wallet, PIN, and environment follow-up

- [x] Publish a stable `/logo.png` endpoint with CORS and cache headers so
  wallet icon URLs do not depend on a source filename.
- [x] Include `GYDS_P2P_ADVERTISE_HOST` in setup-generated `.env` files.
- [x] Load `GYDS_NETWORK_NAME`, `GYDS_WS_PORT`, `GYDS_MAX_PEERS`, and
  `GYDS_LOG_FORMAT` from the environment.
- [x] Allow the setup-generated `GYDS_DASHBOARD_PIN` to bootstrap the stored
  hash once. Existing hashes are never overwritten; the plaintext value may be
  removed from `.env` after initialization.
- [ ] Verify the logo and metadata from the public HTTPS RPC origin in each
  target wallet.
- [ ] Verify joining-node synchronization against a reachable public P2P host.

### Correct RPC diagnostics

The default dedicated JSON-RPC port is `8545`, not `8544`. Use this while
testing unless the node's `.env` explicitly sets `GYDS_RPC_PORT=8544`:

```bash
curl -sS http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' | jq

curl -sS http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"net_peerCount","params":[],"id":1}' | jq

curl -sS http://127.0.0.1:8545/api/peers | jq

curl -sS http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"net_enode","params":[],"id":1}' | jq
```

Acceptance criteria:

- [ ] `net_enode` returns a non-empty node ID and reachable advertised P2P
  address.
- [ ] `net_peerCount` equals the number of connected peers.
- [ ] `/api/peers` shows the genesis node and each joining node.
- [ ] The joining node's height catches up to the genesis node's height.
- [ ] Restarting either node reconnects without manually editing state.

### Wallet storage policy

- [x] The setup wizard's generated/imported wallet key is written server-side
  to `.env` as `GYDS_WALLET_PRIVATE_KEY`; it is not intentionally stored in
  browser local storage.
- [ ] Keep server-side wallet persistence optional. The setup wizard must allow
  `Skip wallet`, and an empty wallet key must remain valid.
- [ ] Protect `.env` with mode `0600` or an equivalent owner-only permission,
  keep it outside the public static directory, and never include private keys in
  API responses, HTML, logs, or backups sent to third parties.
- [ ] Add a clear warning that a server-side private key controls funds and
  should be used only on a secured wallet/validator host.
- [ ] Prefer an encrypted server keystore with an operator-supplied unlock
  secret for production; do not silently generate or persist a private key.
- [ ] Keep browser/MetaMask signing optional. Browser wallets should remain
  self-custodied and should not be copied into server storage.
- [ ] Add a recovery test: restart the node and confirm the optional server
  wallet remains available without putting the key in browser storage.

### PIN configuration policy

- [ ] Add an optional `GYDS_DASHBOARD_PIN` environment setting. Do not store
  the plaintext PIN in logs or API responses.
- [ ] On startup, if `GYDS_DASHBOARD_PIN` is set, validate its length and
  update the hashed `<dataDir>/admin/.pin_hash` atomically.
- [ ] Define empty/unset behavior explicitly: either keep the existing hashed
  PIN unchanged, or provide a separate documented switch to disable the PIN;
  never disable authentication accidentally because an environment variable is
  missing.
- [ ] Allow changing the PIN by editing the server `.env`, then restarting the
  node. Document the exact procedure and ownership/permissions.
- [ ] Make the setup wizard and `.env` behavior consistent. The current setup
  path skips changing the PIN when a hash already exists.
- [ ] Add tests for first-time PIN creation, `.env` PIN rotation, invalid PIN,
  unset PIN, restart persistence, and failed/partial writes.

### Two-server verification runbook

On the genesis server:

```bash
grep -E '^(GYDS_NODE_MODE|GYDS_CHAIN_ID|GYDS_P2P_PORT|GYDS_RPC_PORT|GYDS_P2P_ADVERTISE_HOST|GYDS_PEER_AUTH|GYDS_ALLOWED_NODES)=' /opt/gyds-fullnode/.env
sudo ss -lntp | grep -E ':(30303|8545)\b'
sudo ufw status
curl -sS http://127.0.0.1:8545/api/node-id | jq
curl -sS http://127.0.0.1:8545/api/peers | jq
```

On each joining server:

```bash
grep -E '^(GYDS_NODE_MODE|GYDS_CHAIN_ID|GYDS_P2P_PORT|GYDS_RPC_PORT|GYDS_BOOTSTRAP_NODES|GYDS_PEER_AUTH|GYDS_ALLOWED_NODES)=' /opt/gyds-fullnode/.env
nc -vz GENESIS_PUBLIC_IP 30303
sudo systemctl restart gyds-fullnode
sudo journalctl -u gyds-fullnode -n 200 --no-pager | grep -Ei 'bootstrap|connected|handshake|auth|peer|dial|failed'
curl -sS http://127.0.0.1:8545/api/peers | jq
```

Do not mark P2P complete until the firewall test, handshake logs, peer count,
peer list, and height synchronization all pass on both servers.

## Plan 8 — Make the public RPC wallet-ready

Your public JSON-RPC endpoint is:

```text
https://rpc.netlifegy.com
```

Validation completed on **2026-08-09**:

- [x] Confirm the HTTPS RPC endpoint responds to `eth_chainId`.
- [x] Confirm the endpoint reports chain ID `198282` (`0x3068a`).
- [ ] Publish `https://rpc.netlifegy.com/gyds-network.json` (it currently
  returns `404 Not Found`).
- [ ] Set the production `GYDS_EXTERNAL_URL` to the public HTTPS origin and
  restart the node.
- [ ] Publish the WebSocket endpoint and block explorer URL.
- [ ] Add the network to MetaMask and the other target wallets using the
  verified RPC URL.
- [ ] Test a read-only request and a small test transaction from an external
  wallet.
- [ ] Add RPC health, latency, rate-limit, and restart monitoring.

## Completed in the codebase

- [x] Set the native GYDS genesis supply to exactly **1,000,000,000 GYDS**.
- [x] Keep GYD defined as a stablecoin with 18 decimals and a
  **10,000,000,000 GYD** genesis supply.
- [x] Split the 1B GYDS genesis allocation across the three genesis validator
  addresses: 500M, 300M, and 200M.
- [x] Resolve relative data directories to absolute paths before writing a
  systemd unit.
- [x] Reject whitespace/newline data paths that cannot be represented safely in
  `ReadWritePaths=`.
- [x] Validate every generated `ReadWritePaths=` entry before starting the
  service.
- [x] Keep the dashboard's GYDS and GYD image assets available at
  `/gyds-coin.jpg` and `/gyd-coin.png`.

## Important genesis warning

- [ ] Decide whether this is a new network or an existing network.
- [ ] For a new network, stop the node and remove/replace only the intended
  chain state directory before starting it with the new genesis. Back up the
  old data first.
- [ ] For an existing network, do **not** delete `state.db`. All nodes must use
  the same genesis, and changing the supply requires a coordinated migration
  or an on-chain distribution/mint process. Otherwise nodes can disagree about
  the chain state.
- [ ] Confirm the genesis allocation addresses are the intended treasury and
  validator addresses before production launch.
- [ ] Publish the final genesis hash and supply allocation so every node
  operator can verify the same network.

## External wallet support for native GYDS

- [x] Publish a stable HTTPS RPC URL: `https://rpc.netlifegy.com`.
- [ ] Publish a stable HTTPS WebSocket URL and block explorer URL.
- [x] Add a canonical `/gyds-network.json` document with chain ID, RPC,
  WebSocket, explorer, and logo URLs.
- [ ] Add the network to each wallet using:
  - Chain name: `GYDS Chain`
  - Chain ID: `198282`
  - Native symbol: `GYDS`
  - Decimals: `18`
  - RPC URL: `https://rpc.netlifegy.com`
- [ ] Use HTTPS for `iconUrls`; many wallets ignore HTTP or localhost image URLs.
- [x] Add PNG and JPEG logo URLs to the wallet-add request and metadata document.
- [ ] Submit the network logo to the wallet's supported chain registry where
  required. `wallet_addEthereumChain` does not guarantee that a wallet will
  display the icon.
- [x] Prepare a square PNG logo with a public URL, CORS enabled, no
  authentication, and a one-day cache lifetime.
- [ ] Configure `GYDS_EXTERNAL_URL` to the final HTTPS origin so the public
  metadata URLs and wallet icon URLs are HTTPS in production.
- [ ] Test adding the chain in MetaMask mobile, MetaMask extension, Trust
  Wallet, Coinbase Wallet, and any other target wallet.
- [ ] Remove and re-add the network during testing because wallets cache chain
  metadata and icons.

## External wallet support for GYD stablecoin

GYD is currently a node-managed genesis token, not a standard ERC-20 contract.
Most external EVM wallets cannot discover or display such a token from the
custom `/api/tokens/{address}` endpoint.

- [ ] Decide whether GYD should become an ERC-20-compatible token on GYDS
  Chain, or remain a node-native token with a custom wallet integration.
- [ ] If using ERC-20, implement and independently review the token contract.
- [ ] Define the permanent GYD contract address and deployment procedure.
- [ ] Define how the existing 10B genesis GYD balance maps to contract balances.
- [ ] Prevent double counting between node-managed GYD and contract GYD.
- [ ] Define mint authority, freeze authority, burn rules, pause rules, and
  stablecoin reserve/redemption policy.
- [ ] Add ERC-20 JSON-RPC support and test `balanceOf`, `decimals`, `symbol`,
  `name`, `totalSupply`, `transfer`, `approve`, and `transferFrom`.
- [x] Host `/gyd-token.json` with GYD name, symbol, decimals, supply, logo URL,
  and an explicit no-contract status.
- [ ] After a real ERC-20 contract exists, publish contract-backed GYD metadata
  with its permanent contract address.
- [ ] Register the GYD logo and metadata with target wallet token lists.
- [ ] Test importing GYD by contract address in every target wallet.
- [ ] Publish the stablecoin reserve, issuer, redemption, audit, legal, and
  risk information before representing GYD as a 1:1 stablecoin.

## Deployment error from the uploaded screenshot

The screenshot shows `gyds-fullnode.service` repeatedly failing while systemd
reports an invalid/non-absolute `ReadWritePaths` entry. Run the following on
the server after pulling the updated scripts:

```bash
sudo systemctl stop gyds-fullnode
sudo systemctl reset-failed gyds-fullnode
sudo bash deploy.sh --update
sudo systemctl daemon-reload
sudo systemd-analyze verify /etc/systemd/system/gyds-fullnode.service
sudo systemctl enable --now gyds-fullnode
sudo systemctl status gyds-fullnode --no-pager
sudo journalctl -u gyds-fullnode -n 100 --no-pager
```

If the server uses the other installer, run:

```bash
sudo systemctl stop gyds-fullnode
sudo systemctl reset-failed gyds-fullnode
sudo bash setup-fullnode-server.sh --no-docker --update
sudo systemctl daemon-reload
sudo systemd-analyze verify /etc/systemd/system/gyds-fullnode.service
sudo systemctl enable --now gyds-fullnode
```

Inspect the generated unit if it still fails:

```bash
sudo systemctl cat gyds-fullnode
sudo grep -nE '^(WorkingDirectory|Environment|ReadWritePaths|StandardOutput|StandardError|ExecStart)=' \
  /etc/systemd/system/gyds-fullnode.service
```

Every `ReadWritePaths` value must begin with `/`. Do not manually use
`ReadWritePaths=./data`, `ReadWritePaths=data`, or a path containing spaces.

## Production readiness

- [ ] Back up `.env`, chain state, keystore, node identity, and admin database.
- [ ] Configure DNS and TLS before exposing RPC or wallet endpoints.
- [ ] Restrict administrative dashboard and RPC access with firewall rules,
  reverse proxy rules, or an allowlist.
- [ ] Configure peer authorization and bootstrap nodes.
- [ ] Open P2P TCP and UDP port `30303` only where needed.
- [ ] Confirm NTP/time synchronization on every validator.
- [ ] Add monitoring for RPC health, dashboard health, peer count, disk space,
  memory, and repeated service restarts.
- [ ] Document the recovery procedure and test restoring a backup.
- [ ] Do not advertise a stablecoin peg until reserves, redemption, and
  compliance controls are operational.