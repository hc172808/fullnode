# Mainnet WireGuard peer enrollment

This is a configuration guide, not a deployment or data migration. The current
Replit preview is an RPC-only testnet node; it is not a P2P coordinator. These
settings are for the operator's existing mainnet server and joining P2P nodes.

## Preserve the existing mainnet node

Do not copy `.env.example` over the production `.env` and do not change the
coordinator's `GYDS_DATA_DIR`, node key, network, or role just to enable peer
enrollment. First confirm the existing node's network, genesis, data directory,
and node ID. A testnet database cannot be turned into mainnet by changing only
`GYDS_NETWORK`; the networks have different genesis identities.

The coordinator must run a P2P-enabled role on mainnet with peer authentication
enabled. Keep its existing mainnet data directory and operator settings. The
joining node's registration URL points to the coordinator's WireGuard address:

```env
# Coordinator: retain the current mainnet data path and node role.
GYDS_NETWORK=mainnet
GYDS_PEER_AUTH=true
GYDS_P2P_PORT=30303

# Joining node: register through the coordinator's WireGuard interface.
GYDS_PEER_REGISTRATION_URL=http://10.0.0.1:5000
```

The coordinator does not need a registration URL unless it is itself joining a
different coordinator.

## Joining-node settings

Use the same mainnet genesis as the coordinator, a unique data directory and
node key, and the joining node's own WireGuard address as its P2P advertise
host. Use `sync` for a node that must catch up to the existing chain; use the
appropriate P2P role for its intended operation.

```env
GYDS_NETWORK=mainnet
GYDS_NODE_MODE=sync
GYDS_DATA_DIR=/var/lib/gyds-fullnode-peer
GYDS_PEER_AUTH=true
GYDS_PEER_REGISTRATION_URL=http://10.0.0.1:5000
GYDS_P2P_PORT=30303
GYDS_P2P_ADVERTISE_HOST=<this-node-wireguard-ip>
GYDS_P2P_ALLOW_PRIVATE_ADVERTISE=true
GYDS_BOOTSTRAP_NODES=10.0.0.1:30303
GYDS_ALLOWED_NODES=<coordinator-node-id>
```

Replace the data directory with a unique path for this node and replace the
advertise-host placeholder with its assigned WireGuard IP. Do not reuse another
node's data directory or `node.key`.

The coordinator's node ID is available from its local dashboard at
`http://127.0.0.1:5000/api/node-id`. The coordinator administrator must approve
the joining node under Admin → Peer approvals. The joining node must also trust
the coordinator's ID in its own `GYDS_ALLOWED_NODES`; authorization is local to
each node, so coordinator approval alone does not grant reciprocal trust.

## Network access and safety

- Route the joining node's WireGuard subnet between the nodes. Permit dashboard
  TCP `5000` and P2P TCP `30303` through the VPN; restrict dashboard access to
  trusted VPN/admin access.
- The enrollment service accepts registration only from private/VPN source IPs.
  It uses the request source address and configured P2P port; it does not read
  `/etc/wireguard/wg0.conf` or transfer a WireGuard private key.
- Keep JSON-RPC (`8545`) off the public internet unless it is intentionally
  exposed behind a secured proxy.
- Do not use `GYDS_NODE_MODE=testnode` for persistent production data; testnode
  mode is intended for disposable data.
- Switching an existing node between network profiles is not a database
  conversion. Preserve its current data and use a separate data directory for
  a distinct network.
- Mainnet and P2P settings do not by themselves establish production readiness;
  consensus, validator, migration, and independent-review gates remain separate.
