# GYDS Chain production TODO

## Plan 9 — Multi-node setup, network identity, and automatic maintenance

- [x] Use chain ID `198282` for the live GYDS network and keep the isolated
  test node on chain ID `31337`.
- [x] Provide setup choices for full, lite, RPC, boost, genesis, sync,
  validator, and isolated testnode roles.
- [x] Give each role a documented non-overlapping default dashboard, RPC,
  WebSocket, and P2P port profile while keeping all ports editable.
- [x] Require the dashboard PIN during setup; the server rejects setup saves
  that do not include a valid PIN.
- [x] Apply UFW and fail2ban during deployment when enabled, including the
  selected node ports.
- [x] Check for newer node releases in the running dashboard and provide a
  safe fast-forward update path with backups, health checks, and rollback.
- [x] Add a privileged systemd timer for automatic OS security updates and
  safe node updates; it never reboots or mutates a genesis block.
- [x] Verify the configured genesis hash against peers before accepting them,
  and document that intentional genesis changes require a coordinated reset.

### Network rule

Changing the genesis configuration or chain ID does **not** automatically
change already-running nodes. Those nodes reject incompatible peers. A
genesis change is a new network and requires an explicit coordinated migration
or fresh data directories; silent propagation would risk a split-brain chain.

This checklist records the work still needed for production wallet support and
the deployment failure shown in the uploaded screenshot.

## Master prompt audit — remaining engineering work

- [x] P1 — Add automated RPC compatibility tests for chain ID, syncing, blocks,
  balances, calls, gas estimation, signed raw transactions, pending/confirmed
  receipts, HTTP batch/CORS behavior, and the custom WebSocket block feed.
  These tests do not claim that the separate Ethereum `eth_subscribe` protocol
  is implemented.
- [ ] P1 — Add comprehensive P2P and recovery tests for peer discovery,
  multiple bootnodes, static peers, reconnect, sync, and bootnode failure;
  required to validate multi-node operation rather than only startup.
- [ ] P1 — Add production monitoring and alerting for node/RPC availability,
  sync lag, peer count, validator duties, disk, CPU, RAM, network, and
  repeated restarts; current dashboard health is not external alerting.
- [ ] P1 — Document and test encrypted backup/recovery for chain data, node
  identity, wallet material, and validator keys without exposing secrets.
- [ ] P1 — Put public RPC behind HTTPS/reverse proxy with authentication and
  method restrictions; the built-in RPC is not a substitute for an internet
  edge.
- [ ] P2 — Decide whether a separate archive node, backup RPC node, monitoring
  node, and redundant bootnode are required for the expected traffic and
  availability target.
- [ ] P2 — Complete an independent validator review covering registration,
  voting/attestation, finality, slashing, rewards, lifecycle, and missed-duty
  behavior; document only behavior actually implemented by this PoS engine.
- [ ] P2 — Add an explicit, reviewed genesis-import/migration procedure only if
  external Genesis JSON loading is required; never make JSON replacement
  automatic for an existing chain.

## Current requested work

- [x] Keep exactly two operational scripts: one safe reset script and one Git
  update script that pulls updates, rebuilds, restarts, and rolls back safely.
- [x] Persist every Admin Node configuration and peer connection in the node
  data directory so settings and sync peers survive restarts and updates.
- [x] Retry saved peers after startup even when a peer is temporarily offline.
- [x] Keep dashboard, JSON-RPC, and P2P listeners on distinct ports in the
  development workflow and deployment configuration.
- [x] Make wallet network onboarding use the correct dedicated RPC endpoint and
  stable logo metadata; document native GYDS versus a contract token.
- [x] Build and restart the node, then verify health, dashboard, RPC, and
  persistence behavior.
- [x] Use `https://explorer.netlifegy.com` as the canonical explorer URL for
  every node and wallet network configuration.
- [x] Use `https://rpc.netlifegy.com` as the canonical public RPC endpoint for
  RPC-node setup and wallet network configuration.

## Plan 8 — Reset, Git updates, persistent peers, and wallet onboarding

### Deployment scripts

- [x] Add a guarded reset script that stops the managed node, removes the
  server `.env` and configured runtime data, and restarts into the setup wizard.
- [x] Make reset support native systemd, Docker Compose, and explicit
  application/data directories without allowing dangerous paths.
- [x] Require confirmation or `--yes` for destructive resets and print exactly
  what will be deleted.
- [x] Make the update script pull the configured Git branch, fast-forward only,
  rebuild/test, restart, and roll back on failed health checks.
- [x] Preserve `.env`, chain data, node identity, admin state, keystore, and
  imported peers during updates.
- [x] Install and document exactly two operational scripts: reset and update.

### Persistent node connections

- [x] Persist peers added from the Admin Node panel or imported node config into
  `GYDS_BOOTSTRAP_NODES` so they return after a restart.
- [x] Validate sync mode before saving so it cannot restart without a bootstrap
  peer and become unavailable.
- [x] Show persisted bootstrap peers separately from currently connected peers.

### Wallet onboarding and logos

- [x] Make “add GYDS Chain” use a reachable public HTTPS origin for RPC and
  logo URLs, with a manual fallback for wallets that reject EIP-3085.
- [x] Clarify that native GYDS is a network currency while GYD is separate and
  cannot be imported as an ERC-20 token without a real contract address.
- [x] Confirm `/logo.png` is stable, publicly reachable, square, and listed in
  network metadata; document that wallets may ignore or cache icons.

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
- [x] Start the P2P listener before outbound bootstrap dialing; the sync role
  fans bootstrap attempts out concurrently after the listener is ready.
- [x] Log the configured bootstrap address, resolved address, dial error, local
  node ID, remote node ID, chain ID mismatch, and successful handshake.
- [x] Reject peers with a different chain ID or genesis hash. When the local
  genesis hash is configured, peers that omit it are rejected as unverifiable.
- [x] Remove peers through identity-checked disconnect cleanup; peer counts and
  `/api/peers` omit unauthorized or closed connections.
- [x] Add focused tests for `net_enode`, advertised-host validation, chain and
  genesis mismatch, live authorized peer counts, and disconnect cleanup.
- [ ] Complete P2P integration tests for inbound/outbound dialing, retry and
  reconnect behavior, duplicate node keys, peer authorization, and multi-node
  block synchronization.

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

## Plan 9 — Wallet persistence, branding, and the two-coin model

### Confirmed behavior

- [x] Setup is server-side. Opening the dashboard from another device does not
  restart the setup wizard; the device may still need the Admin PIN/login.
- [x] Keep node configuration and wallet state under the persistent
  `GYDS_DATA_DIR`; operators must persist that directory across restarts and
  container replacement.
- [x] Publish stable network metadata and a stable `/logo.png` URL for wallets
  that support custom network icons.
- [x] Document that native GYDS has no contract address, just as ETH has no
  ERC-20 contract address on Ethereum.
- [x] Document that GYD currently has no contract address and is not yet
  wallet-importable as a standard ERC-20 token.
- [x] Document the current genesis supplies: 1,000,000,000 GYDS and
  10,000,000,000 GYD.
- [ ] Verify `/gyds-network.json`, `/gyd-token.json`, and `/logo.png` from the
  public HTTPS RPC origin in each target wallet. Metadata availability does
  not guarantee that a wallet will display the icon.

### Supply and wallet-token decisions required before implementation

- [x] Product decision: allow authenticated Admin mint/burn on the live
  network, subject to consensus-safe authorization and an auditable supply
  policy. Never make live supply changes an unaudited dashboard field.
- [x] Product decision: GYD should become a standard ERC-20 contract with a
  permanent contract address.
- [ ] Replace the current simplified transaction path with consensus-backed
  signed transaction decoding and execution. `eth_sendRawTransaction` currently
  indexes a placeholder transaction instead of executing it.
- [ ] Implement a production EVM-compatible contract state path. The current
  custom VM does not yet provide Ethereum-compatible selectors, calldata
  semantics, contract deployment, storage commitment, or consensus replication
  sufficient for a real ERC-20.
- [ ] Implement and test ERC-20 `name`, `symbol`, `decimals`, `totalSupply`,
  `balanceOf`, `transfer`, `approve`, `allowance`, `transferFrom`, `mint`, and
  `burn` behavior with standard ABI encoding and event logs.
- [ ] Make Admin mint/burn submit a signed/consensus transaction or governance
  action replicated by every node. Do not mutate a local JSON file or local
  account map as a substitute for chain state.
- [ ] Deploy the GYD contract once on the target chain and record its resulting
  permanent address. Until deployment and verification pass, `/gyd-token.json`
  must continue to omit `contractAddress` and report `walletImportable: false`.
- [ ] If GYD becomes an ERC-20 token, define the mint authority, pause/freeze
  policy, maximum supply policy, deployment block, metadata URI, and migration
  path for existing GYD balances before deploying it.
- [ ] After those decisions, add Admin controls, wallet import metadata, tests,
  and migration documentation together. Do not fabricate a contract address
  before the contract is deployed on the target chain.

## Plan 10 — Genesis-only treasury authority

### Product decision

- [x] Only the configured `genesis` node may propose GYD supply operations.
- [x] Other nodes must never expose a local mint/burn control and must only
  accept an operation after validating and replicating it from the chain.
- [x] Treat the issuer as a treasury authority, not as an ordinary wallet
  balance. The treasury must have a documented starting allocation, authority
  key, and audit history.

### Recommended treasury model

- [ ] Create a dedicated treasury address separate from the genesis node's
  operator wallet and validator reward wallet.
- [ ] Keep the treasury signing key offline or behind a protected signing
  process. Do not place the private key in every node's `.env`, browser
  storage, or wallet UI.
- [ ] Prefer a multisignature or threshold approval process for production
  mint/burn requests. If the first release uses one genesis authority, make
  that a documented transitional policy with a key-rotation path.
- [ ] Define whether treasury funds are spendable GYD reserves, an issuer
  balance, or unissued supply. Never silently mix treasury funds with total
  supply accounting.

### Genesis-only issuance rules

- [ ] Add a signed, consensus-visible `Mint`/`Burn` transaction type with the
  genesis treasury authority and chain ID bound into the signed payload.
- [ ] Reject mint/burn transactions received from every non-genesis node,
  including forged transactions using the genesis node's network address.
  Authorization must be cryptographic, not based only on node mode or IP.
- [ ] Enforce nonce, replay protection, maximum amount, and optional daily or
  epoch issuance limits.
- [ ] Require every node to validate the authority signature, current supply,
  destination address, amount, and operation sequence before applying state.
- [ ] Broadcast confirmed issuance operations so joining nodes replay the same
  state transitions and reach the same total supply.
- [ ] Expose the treasury address, total supply, issued supply, and operation
  history as read-only public data. Never expose the private key.
- [ ] Show mint/burn status and transaction hashes in the authenticated Admin
  dashboard only on the genesis node.

### Acceptance tests

- [ ] A mint request from the genesis authority is accepted, included in a
  block, and produces the same balances and total supply on every node.
- [ ] A request from a full, sync, validator, lite, or RPC node is rejected
  even when it targets the GYD contract address.
- [ ] A forged or replayed genesis issuance request is rejected.
- [ ] Restarting the genesis node preserves the treasury nonce, supply, and
  audit history.
- [ ] A new node can synchronize all historical issuance operations without
  receiving any private key.
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
custom `/api/tokens/{address}` endpoint. The USD peg is a product target only:
there is no implemented or independently verified USD reserve, redemption
guarantee, or ERC-20 contract yet. Do not describe GYD as backed, redeemable, or
already maintaining a $1 price.

- [x] Product decision: GYD's intended peg is **1 GYD = 1 USD**, not 1 GYD = 1
  Guyana Dollar. The peg is not operational or verified yet.
- [x] Product decision: publish a stable, public HTTPS URL for the GYD token
  logo. Keep it distinct from the GYDS network logo URL.
- [x] Use a standard ERC-20 contract for wallet-compatible GYD and
  user-created tokens; the ERC-20 standard itself does not define a logo field.
- [ ] Define the USD reserve/collateral model, custodian, attestations/audits,
  redemption eligibility and process, issuance/burn controls, fees, depeg
  response, legal/compliance obligations, and disclosures. Do not claim a
  reliable USD peg until those controls are real and independently verified.
- [ ] Change the dashboard copy that currently says GYD is pegged 1:1 to the
  Guyana Dollar and claims auditable reserves. Until the USD peg and backing are
  operational, label USD parity as the target only and state what is not yet
  implemented.
- [ ] Define the permanent GYD contract address and deployment procedure.
- [ ] Define how the existing 10B genesis GYD balance maps to contract balances.
- [ ] Prevent double counting between node-managed GYD and contract GYD.
- [ ] Define mint authority, freeze authority, burn rules, pause rules, and
  consensus-safe USD-token issuance/redemption policy.
- [ ] Add ERC-20 JSON-RPC support and test `balanceOf`, `decimals`, `symbol`,
  `name`, `totalSupply`, `transfer`, `approve`, and `transferFrom`.
- [x] Host `/gyd-token.json` with GYD name, symbol, decimals, supply, logo URL,
  and an explicit no-contract status.
- [ ] Point GYD's `logoUrl` to a GYD-specific public HTTPS image URL such as
  `/gyd-coin.png`; do not reuse `/logo.png`, which is the GYDS network logo.
  Confirm HTTPS, correct image type, anonymous access, CORS, stable caching, and
  that the URL keeps working after deploys and asset changes.
- [ ] After a real ERC-20 contract exists, publish contract-backed GYD metadata
  with its permanent contract address and the canonical public logo URL.
- [ ] Register the GYD contract, metadata, and logo with each chosen wallet's
  token list or asset registry where required. A metadata URL or ERC-20 contract
  alone does not make every wallet show a token image.
- [ ] Test adding GYD by contract address in every explicitly supported wallet;
  record which display its logo, caching delays, approval requirements, and
  manual-import fallback. Do not promise support for arbitrary wallets because
  logo discovery and registry policies are wallet-specific.
- [ ] Publish verified USD reserve, issuer, redemption, audit, legal, and risk
  information before representing GYD as backed or maintaining a $1 USD peg.

## Future — User-created tokens and token-management website

The Go `gpl/token` package has standalone token operations, including creation,
minting, burning, allowances, account freezing, and mint-authority changes.
However, no current Go call sites connect those operations to chain
transactions, consensus, or the RPC API. The current token RPC endpoints expose
genesis-token data only. The custom VM is not yet a production EVM contract
runtime, so the package alone is not enough to issue wallet-compatible tokens.

- [x] Product decision: user-created assets will use standard EVM ERC-20
  contracts.
- [ ] Define target wallet compatibility, contract-address/deployment rules,
  metadata, decimals, and supply limits. Coordinate the EVM work with the
  existing GYD ERC-20 migration plan above.
- [x] Make `eth_sendRawTransaction` fail closed rather than return fabricated
  transaction hashes and pending records. It remains unavailable until signed
  transaction decoding, mempool admission, and consensus execution exist.
- [ ] Connect token creation and every state-changing operation to signed,
  chain-ID-bound transactions that are validated and replayed identically by
  every node. Use deterministic block data rather than wall-clock values, and
  include nonce/replay protection, fees, persisted state, and transaction
  receipts/events.
- [ ] Define the authority model before implementation: creator/owner,
  minting, burning, freezing/unfreezing, optional pausing, authority transfer
  or renunciation, and any maximum-supply rules. The current module has mint
  and freeze authorities but no freeze-authority transfer or pause operation;
  authority changes and freezes also need complete event history.
- [ ] Enforce each authority in consensus execution using the transaction
  signer, not a web-server admin session or an unsigned API request. Make
  irreversible actions such as revoking an authority explicit in the UI.
- [ ] Add RPC/API reads for token metadata, supply, balances, authorities,
  status, and operation history. If ERC-20 is selected, implement standard ABI
  behavior and logs only after the production EVM path is ready.
- [ ] Build the wallet-connected website to create tokens and manage only the
  authorities assigned to the connected wallet. Have wallets sign transactions
  locally; never send private keys to the website or node.
- [ ] Test unauthorized operations, invalid signatures, replay protection,
  deterministic state across multiple nodes, restart/sync recovery, supply
  accounting, and wallet compatibility for the selected token format.

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

## Blockchain completion roadmap — implementation is not production approval

This checklist describes the additional engineering needed to assess and harden
the Go blockchain before treating it as a production-grade public network.
Complete each item with tests, peer/operator documentation, and independent
review where security or consensus is involved. A green build, running
dashboard, or single-node test does not establish production readiness. Keep
the existing genesis, chain ID, balances, and token policy unchanged unless a
coordinated network migration is explicitly approved.

### P0 — Consensus, state, and transaction safety

- [ ] Publish a concise protocol specification for block format, transaction
  encoding, chain ID, genesis hash, validator membership, block timing,
  execution rules, state roots, fork choice/finality, and upgrade rules. Make
  implementations and tests agree with that specification.
- [ ] Independently audit block validation and fork choice. Verify parent links,
  height, timestamps, validator authorization, duplicate blocks, reorg limits,
  finality claims, and rejection of conflicting histories.
- [ ] Make every state transition deterministic across machines and Go/runtime
  versions. Remove wall-clock, local filesystem, map-iteration, and
  node-configuration dependence from consensus results; specify timestamp and
  integer-overflow rules.
- [ ] Define and enforce a complete signed transaction envelope: chain ID,
  account nonce, signature recovery, sender, destination, value, data, gas
  limit/price, replay protection, size bounds, and rejection conditions.
- [ ] Make mempool admission, replacement, eviction, nonce ordering, fee policy,
  and pending reads consistent with block execution. Prevent a transaction
  accepted by RPC from producing a different result when proposed or replayed.
- [ ] Prove that every node independently re-executes blocks and derives
  identical account/storage state, receipts, gas accounting, and committed
  roots. Test bad signatures, wrong chain IDs, underfunded senders, invalid
  nonces, reverted calls, duplicate transactions, and malformed encodings.
- [ ] Finish the production EVM path before claiming general Ethereum
  compatibility: fork rules and precompiles, contract creation, bytecode
  execution, storage, logs, revert behavior, gas schedules, block context,
  account/code/storage roots, upgrades, and historical reads. Run suitable
  Ethereum execution tests and third-party wallet/tooling fixtures.
- [ ] Decide whether consensus is a custom PoS protocol or an Ethereum
  consensus-compatible protocol. Document validator selection, quorum/finality,
  liveness, equivocation, slashing, recovery, and economic assumptions; obtain
  an independent protocol/security review before production launch.
- [ ] Add coordinated, signed protocol-upgrade activation (version signaling,
  activation height, compatibility window, operator notice, and rollback
  boundary). Do not let a normal Git update silently change consensus behavior.

### P1 — Peer network and node recovery

- [ ] Finish the P2P and recovery test plan above with real multi-process nodes:
  inbound/outbound handshakes, peer authentication, multiple bootnodes,
  persistent peers, reconnect, peer disconnect cleanup, bootnode outages,
  incompatible genesis/chain IDs, and catch-up after restart.
- [ ] Demonstrate multi-node block and transaction propagation and identical
  heights, block hashes, receipts, and state roots through restart and network
  partition/rejoin scenarios. Record reproducible commands and expected output.
- [ ] Specify and harden the P2P wire protocol: message versioning, framing,
  size/time limits, flow control, request correlation, duplicate suppression,
  peer scoring, rate limits, and safe parsing of untrusted messages.
- [ ] Add protection and tests for connection floods, oversized/malformed
  messages, unauthorized identities, replayed handshakes, eclipse/Sybil risks,
  and resource exhaustion. Document which risks remain unsolved.
- [ ] Add peer discovery only with authenticated/validated records and a clear
  network identity; make static bootstrap peers and safe fallback behavior
  continue to work when discovery is unavailable.

### P1 — Storage, restart, and migration

- [ ] Specify the on-disk database schema and versioning. Make migrations
  atomic, restart-safe, backward-compatible for the supported upgrade window,
  and safe when interrupted or repeated.
- [ ] Add crash/restart tests at block commit, transaction indexing, state
  update, and migration boundaries. On recovery, verify canonical height,
  block hashes, account state, receipts, and pending-transaction policy.
- [ ] Test database corruption, disk-full, permission, and I/O failures. Fail
  visibly and safely; never silently reset a live chain to genesis or continue
  with an inconsistent partial state.
- [ ] Document and test offline, encrypted backups and restoration for chain
  data, configuration, admin recovery, node identity, validator signing
  material, and any operator-held wallet. Define retention and recovery-time
  objectives without sending private keys to a third party.
- [ ] Define archival, pruning, and state-history requirements. Do not prune
  data needed by wallet balance queries, block/receipt lookups, audits, or
  reindex/recovery unless the node role clearly documents the limitation.

### P1 — RPC, wallets, and API truthfulness

- [ ] Maintain an explicit RPC support matrix. Implement methods only when
  their outputs are real and consistent; return documented errors for
  unsupported execution, filters, subscriptions, historical state, traces, or
  syncing rather than fabricated success values.
- [ ] Test standard `eth_subscribe`/`eth_unsubscribe` notifications separately
  from the existing custom `/api/ws` block-event feed, including reconnect,
  subscription cleanup, slow consumers, and multiple simultaneous clients.
- [ ] Complete RPC compatibility for blocks/transactions/receipts, pending vs.
  confirmed nonce/balance, gas/fee methods, historical tags, filters, logs,
  errors, and HTTP/WebSocket behavior. Compare outputs with independent
  Ethereum clients and libraries.
- [ ] Implement externally safe RPC controls: HTTPS termination, origin and
  method policy, request/body/time limits, rate limits, abuse monitoring,
  administrative route isolation, and documented exposure defaults.
- [ ] Verify canonical chain metadata, HTTPS RPC, WebSocket, explorer, and logo
  endpoints from the public internet and test wallet onboarding on each named
  wallet. Keep a manual setup path where wallet APIs are unsupported.

### P1 — Validator keys, access, and operational security

- [ ] Define separate roles and key lifecycles for node identity, validator
  signing, treasury issuance, dashboard administration, and user wallets.
  Provide rotation/revocation/recovery procedures and least-privilege access.
- [ ] Never return or log private keys, PINs, seed phrases, auth challenges, or
  uploaded secrets. Audit APIs, HTML, errors, telemetry, backups, and access
  logs for accidental disclosure.
- [ ] Ensure validator signing keys are not automatically generated, copied
  across nodes, or stored as plaintext in the dashboard or general-purpose
  `.env`. Document protected signing/HSM options and limits of the current
  implementation.
- [ ] Independently test dashboard/Web3 authentication, session expiration,
  CSRF/origin policy, authorization on every state-changing endpoint, brute
  force protections, and recovery without disabling authentication.
- [ ] Add dependency updates, reproducible builds, signed release artifacts,
  vulnerability response, and a documented security disclosure process.

### P1 — Monitoring and release readiness

- [ ] Add actionable alerts for RPC/dashboard availability, sync lag, peer
  health, block production/finality, validator missed duties, reorgs, disk
  space, memory/CPU, database errors, and repeated restarts. Test alert delivery
  and avoid logging secrets.
- [ ] Set production SLOs and run load, soak, restart, disk-growth, and
  failure-recovery tests at expected transaction/peer counts. Publish measured
  hardware and bandwidth requirements for each node role.
- [ ] Provide an operator runbook for node provisioning, genesis verification,
  firewall/TLS, validator start/stop, upgrades, incident response, backups, and
  recovery. Verify commands on clean hosts rather than relying on one existing
  server.
- [ ] Obtain independent code, cryptography, consensus, and deployment-security
  reviews; track and resolve findings before mainnet-sensitive releases.

### P2 — User-created ERC-20 token logos and metadata

- [ ] Keep user-created tokens as standard ERC-20 contracts as decided; do not
  imply the ERC-20 interface itself stores or exposes a logo. `name`, `symbol`,
  `decimals`, and `totalSupply` do not standardize token images.
- [ ] For GYD, use the approved USD target (not the Guyana Dollar) and do not
  describe the target as active, backed, or redeemable until the reserve,
  redemption, legal, and independent-verification requirements in the GYD
  wallet section are complete.
- [ ] Decide how the token-creation website accepts and validates a logo, then
  stores it durably at a public HTTPS URL or content-addressed location. Do not
  put large image bytes in contract storage or expose private upload paths.
- [ ] Define and publish token metadata keyed by chain ID and contract address,
  including verified name/symbol/decimals, logo URL, metadata revision, and
  provenance. Specify how updates are authorized and how broken URLs are
  handled.
- [ ] Choose a wallet-discovery path: publish a token list/registry and submit
  assets to the registries used by target wallets, or use a token metadata
  standard only where the target wallets explicitly support it. A website
  upload alone does not update a wallet's token list.
- [ ] Make the token site clearly distinguish “logo uploaded,” “metadata
  published,” and “wallet registry accepted.” Test visibility by contract
  address in each target wallet; expect some wallets to require manual
  import, registry approval, caching delays, or to ignore token logos.
- [ ] Add abuse controls for impersonation, prohibited images, oversized or
  deceptive uploads, ownership verification, URL availability, and metadata
  changes. Preserve a transparent history of metadata updates.

### Definition of “complete”

- [ ] Before calling this chain production-ready, publish a versioned
  specification and an implementation/limitations matrix; pass automated
  unit, property, fuzz, integration, multi-node, restart, and compatibility
  tests; complete independent reviews; test backup restoration and failure
  recovery; and publish operator and wallet runbooks.
- [ ] Record every deferred protocol or RPC feature as an explicit limitation.
  Do not market this Go codebase as fully Ethereum-compatible or as a finished
  production blockchain until the relevant execution, consensus, security, and
  multi-node criteria above have been verified.