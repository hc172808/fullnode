# GYDS Chain Fullnode

## Project overview
A self-hosted, Ethereum-compatible PoS blockchain full node written in Go. Provides:
- **Dashboard** (port 5000): web UI for block explorer, wallet, node admin, and setup wizard
- **JSON-RPC** (port 8545): Ethereum-compatible API (MetaMask, ethers.js, web3.js)
- **P2P** (port 30303): TCP peer-to-peer gossip network
- **WebSocket** (port 8546): real-time subscriptions

## How to run
```
GOTOOLCHAIN=local GYDS_DASHBOARD_PORT=5000 GYDS_RPC_PORT=8545 GYDS_NODE_MODE=full GYDS_DATA_DIR=./data go run . start
```
Or use the "Start application" workflow (already configured).

## Node modes (set via GYDS_NODE_MODE)
| Mode | Description |
|------|-------------|
| `full` | Complete node — P2P, PoS block production, RPC, dashboard. Default. |
| `lite` | Header-only sync. ~95% less storage. No block production. |
| `rpc` | API-only — no P2P, no block production. |
| `boost` | High-performance validator with extended peer limits. |
| `genesis` | Network bootstrapper and initial validator. Seeds the network. |
| `sync` | Catch-up sync from bootstrap peers then runs as a full node. |
| `validator` | PoS block producer with a dedicated GYDS_VALIDATOR_KEY. |
| `testnode` | Ephemeral isolated node — data wiped on restart, no P2P, 5 s blocks. |

## Key environment variables
```
GYDS_CHAIN_ID=198282
GYDS_NODE_MODE=full
GYDS_DASHBOARD_PORT=5000
GYDS_RPC_PORT=8545
GYDS_P2P_PORT=30303
GYDS_P2P_ADVERTISE_HOST=   # public IP/DNS peers should dial
GYDS_DATA_DIR=./data
GYDS_BLOCK_TIME=120         # seconds
GYDS_BOOTSTRAP_NODES=       # comma-separated tcp://host:port
GYDS_VALIDATOR_KEY=         # hex private key for validator signing
GYDS_ENABLE_FIREWALL=true
GYDS_LOG_LEVEL=info
GYDS_LOG_FORMAT=json
```
Sensitive values (wallet key, validator key) are stored in `.env` (mode 0600).

The launcher loads `.env` before starting, so the selected `GYDS_NODE_MODE` is
used after restart. For a genesis node, set `GYDS_NODE_MODE=genesis` and leave
`GYDS_BOOTSTRAP_NODES` empty.

## Setup wizard
Visit `/setup` for the 8-step guided configuration wizard:
1. Node Identity (chain ID, network name, block time, node mode)
2. Wallet (generate or import)
3. Ports & Networking (RPC, WS, P2P, bootstrap peers, peer auth)
4. Storage (data directory, limit)
5. Firewall & Security (UFW)
6. Dashboard PIN (set during setup; optional, and never prompted on the dashboard)
7. Logging (level, format)
8. Review & Save (writes `.env`, applies PIN)

## Security
- Dashboard PIN: optional; SHA-256 hashed, stored at `<dataDir>/admin/.pin_hash`. It can only be created during setup wizard step 6. If unset, the dashboard remains unlocked.
- Admin session: 8-hour cookie, IP-based rate-limit (5 attempts / 15 min lockout).
- Firewall: UFW is the required boundary and is the only firewall layer configured by `deploy.sh`.
- Optional fail2ban configuration remains available only through a separate, explicit `setup-firewall.sh` invocation.
- Peer auth: optional ed25519 challenge-response whitelist (GYDS_PEER_AUTH + GYDS_ALLOWED_NODES).

## Auto-update checker
Background goroutine checks GitHub releases every 24 hours. Status available at `GET /api/updates`.

## Production startup and updates
The production installer enables the required boot services:
- Docker (when using the default Docker deployment)
- `gyds-fullnode-compose.service` for the Docker node, or `gyds-fullnode.service` for native mode
- Nginx reverse proxy
- UFW firewall rules when enabled

After installation, pull and apply the latest Git version with:
```bash
sudo gyds-fullnode-update
```
The updater uses a lock, refuses dirty or non-fast-forward checkouts, runs tests/builds before stopping the node, backs up chain state and `.env`, verifies both RPC and dashboard health, and rolls back the code/image if health checks fail. It does not wipe chain data.

## Genesis node
The genesis config is baked into `core/genesis.go`. If you change it (validators, chain ID, initial balances), **all nodes must be rebuilt from the same binary** to share a common genesis hash. Existing data directories must be wiped if the genesis changes.

## Deploy to production
```bash
bash deploy.sh
```
`deploy.sh` builds the binary, installs it as a systemd service, and applies UFW-only rules (requires root). It does not install or configure fail2ban.

### Deploy a specific node mode
Set the mode in the `.env` file before deploying. The installer persists the selected mode in the installed systemd service and restarts the node with that mode:
```bash
# Network bootstrapper / initial validator
GYDS_NODE_MODE=genesis bash deploy.sh --env .env

# API and dashboard only; no P2P or block production
GYDS_NODE_MODE=rpc bash deploy.sh --env .env
```
For a genesis node, leave `GYDS_BOOTSTRAP_NODES` empty. For an RPC node, point `GYDS_DATA_DIR` at the chain data it should read. Verify the active mode with:
```bash
curl http://127.0.0.1:5000/api/status
```
The response includes `nodeMode`, and the dashboard badge uses the same live value.

## Project structure
```
main.go             — entry point, node mode dispatch
config/config.go    — Config struct, FromEnv()
core/               — chain, blocks, genesis, state
consensus/          — PoS engine, validator set
p2p/                — TCP peer network, node keypair, peer auth
rpc/                — dashboard HTTP server, JSON-RPC, setup wizard, admin
  static/           — HTML/JS/CSS for dashboard and setup wizard
storage/            — LevelDB wrapper
mempool/            — transaction pool
```

## User preferences
- Keep project structure as-is (Go modules, embedded static files)
- Preserve all node modes — do not remove or merge them
- Dashboard PIN must always be set during setup wizard (step 6), not on first dashboard visit
