---
name: Peer authorization system
description: How GYDS node P2P auth works — keypair, challenge-response, whitelist, and env config
---

## The rule
Every node auto-generates (or loads) an **ed25519 keypair** from `<dataDir>/node.key` on startup. The Node ID = hex(pubkey), 64 chars.

When `GYDS_PEER_AUTH=true`:
- Inbound connections receive a `MsgAuthChallenge{nonce}` immediately after the initial handshake
- The connecting node must reply with `MsgAuthResponse{nodeId, signature}` where `signature = ed25519.Sign(privKey, nonceBytes)`
- Server verifies the sig via `VerifyNodeSig(nodeID, nonce, sig)`
- If `GYDS_ALLOWED_NODES` is set (comma-separated), only listed node IDs pass; empty = any valid identity is accepted
- Failed auth → `MsgAuthDenied{reason}` + connection dropped

`MsgGetBlocks` and custom `onMsg` callbacks are only dispatched to **authorized** peers.

## Wire points
- `p2p.LoadOrCreateNodeKey(dataDir)` — called by `wireAuth()` in `main.go` before `p2p.Server.Start()`
- `srv.SetAuth(nk, requireAuth, allowedIDs)` — configure auth on the server
- `srv.NodeID()` — returns this node's ID (exposed via `/api/node-id`)
- `srv.Peers()` — returns `[]PeerStatus` with `Authorized bool` per peer

## Config env vars
- `GYDS_PEER_AUTH=true` — enable the guard
- `GYDS_ALLOWED_NODES=hexId1,hexId2,...` — whitelist; empty = allow any authenticated node

## Dashboard
- Node ID appears in the stats strip with an `AUTH` badge; click to copy full ID
- `/api/peers` now returns real peer data with `authorized` field per peer
- `/api/node-id` returns `{"nodeId": "..."}` for operators to share

**Why:** Permissioned networks need to prevent unauthorized nodes from connecting and receiving chain data.
