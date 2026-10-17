# GYDS Testnet 3B server setup

This runbook is for a new two-server testnet with chain ID `198284`. It does
not change the existing testnet (`198281`) or mainnet (`198282`), and it does
not require resetting either network.

## Genesis contents and current limits

- Genesis supply is 3,000,000,000 native GYDS, split 50/30/20 across the
  existing test addresses.
- GYD starts with zero supply. This profile does not add a minting route;
  on-demand GYD issuance still needs an authorized, auditable policy.
- The allocation addresses are existing test addresses, not wallet addresses
  supplied by the operator. Do not treat their balances as production
  allocations.
- This is a testnet launch, not a public mainnet production approval. Consensus
  and EVM production gates in `todo.md` remain open.

## Prepare the two servers

1. Provision two Linux servers. Use the same repository commit on each one and
   allow TCP `30303` between their reachable addresses in both host and provider
   firewalls. Do not open UDP `30303`; this node does not use it.
2. On each server, copy the matching example file to `.env`:
   - Genesis server: `examples/testnet3b-genesis.env.example`
   - Full node: `examples/testnet3b-full-node.env.example`
3. Replace the `REPLACE_WITH_...` values with actual addresses reachable by the
   other server. Set `GYDS_P2P_ALLOW_PRIVATE_ADVERTISE=true` only if both
   machines can route to the chosen private address. Keep JSON-RPC bound to
   `127.0.0.1` unless it is placed behind a secured HTTPS reverse proxy.
4. Install the genesis server first:

   ```bash
   sudo bash deploy.sh --env .env
   ```

   Keep its peer allowlist empty for this first start. Read its node ID locally:

   ```bash
   curl --fail --silent http://127.0.0.1:8545/api/node-id
   ```

5. Install the full node with its own local data directory and its bootstrap
   address set to the genesis server's reachable `host:30303`. The first
   connection will be rejected while the genesis allowlist is empty. Read the
   full node's ID from its own local `/api/node-id` endpoint.
6. Add the full node ID to the genesis server's `GYDS_ALLOWED_NODES`, and the
   genesis node ID to the full node's `GYDS_ALLOWED_NODES`. Reapply each
   configuration with `sudo bash deploy.sh --env .env --update`.
7. Verify chain ID `198284`, matching genesis identity, live authorized peers,
   block propagation, and sync height on both machines. Restart both services
   and verify that each node retains its own identity and peer configuration.
   Never copy `node.key` from one server to the other.

The addresses are intentionally placeholders because the servers have not
been provisioned yet. The network is not considered connected until these live
checks pass.

## Safe updates and resets

- For a Git update, use the installed updater:
  `sudo gyds-fullnode-update`. It pulls the configured branch, rebuilds, backs
  up, health-checks, and rolls back on failure. Run it on both servers and keep
  them on the same revision.
- `sudo bash deploy.sh --update` rebuilds the checkout already on that server;
  it does not pull from Git. It preserves the `.env`, node identity, and chain
  data.
- Normal updates never reset chain data. A deliberate reset is separate:
  `sudo bash deploy.sh --env .env --reset-data`, then type `RESET` when asked.
  For a non-interactive reset, pass `--confirm-reset-data RESET`. The configured
  data directory is archived before an empty one is created. Check that `.env`
  selects `testnet3b` and `/var/lib/gyds-fullnode-testnet3b` before resetting.
- Never reset mainnet or the existing testnet as part of setting up this new
  network. A code update also must not silently change the `198284` genesis
  allocation; any future genesis change requires a coordinated new data
  directory and operator decision.
