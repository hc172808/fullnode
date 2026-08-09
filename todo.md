# GYDS Chain production TODO

This checklist records the work still needed for production wallet support and
the deployment failure shown in the uploaded screenshot.

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

- [ ] Publish a stable HTTPS RPC URL, WebSocket URL, and block explorer URL.
- [ ] Add the network to each wallet using:
  - Chain name: `GYDS Chain`
  - Chain ID: `198282`
  - Native symbol: `GYDS`
  - Decimals: `18`
  - RPC URL: the final HTTPS RPC endpoint
- [ ] Use HTTPS for `iconUrls`; many wallets ignore HTTP or localhost image URLs.
- [ ] Submit the network logo to the wallet's supported chain registry where
  required. `wallet_addEthereumChain` does not guarantee that a wallet will
  display the icon.
- [ ] Prepare a square PNG logo with a stable public URL, CORS enabled, no
  authentication, and a long cache lifetime.
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
- [ ] Host a public GYD token metadata JSON with name, symbol, decimals, logo
  URL, description, and contract address.
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