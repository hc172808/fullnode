# Draft: CometBFT migration design for GYDS Chain

**Status:** Design only. The operator authorized preparing this design, not
selecting CometBFT as the production engine, changing consensus, activating
mainnet EVM, or modifying mainnet history.

## Confirmed requirements and guardrails

- Target network: mainnet chain ID `198282`; readiness work only.
- Existing GYD: migrate the existing nominal 10B supply one-to-one; do not
  create a second 10B supply.
- Mint authority: a multisig treasury. No mainnet multisig is currently
  deployed, and no public authority address is available.
- Preserve existing mainnet and persistent-testnet data. Do not reset genesis,
  reuse the old database with a new storage engine, or activate a protocol
  upgrade without coordinated operator approval.
- CometBFT v0.40.0 is the design candidate only. The isolated prototype and
  three-validator empty-block test do not establish production readiness.
- Never request or store the operator's personal wallet key or multisig signer
  keys.

## Current implementation findings

1. **The production node still uses the custom proposer.** `main.go` creates a
   `consensus.ValidatorSet` from genesis addresses and starts
   `consensus.NewPoSEngine`. `consensus/pos.go` selects a proposer by block
   number modulo validator count. The configured validator key is not wired into
   that engine.
2. **The current block path does not prove validator authorization or
   finality.** `core.Block` has a validator address but no consensus commit
   proof. `core.Chain.InsertBlock` checks the parent hash and next height; the
   legacy block path does not verify a validator signature or quorum. Block
   creation also uses the local wall clock.
3. **Networking and storage are GYDS-specific.** The full node starts its own
   P2P server and block provider. `core/persist.go` stores custom blocks and
   account balance/nonce data in LevelDB under `state.db`; EVM state, when
   active, has a separate persistent store.
4. **The production EVM path is not active.** Mainnet and persistent testnet
   profiles leave `EVMActivationBlock` unset. The current EVM engine starts from
   `AllEthashProtocolChanges` and sets Shanghai/Cancun activation to timestamp
   zero rather than using a reviewed per-network fork schedule.
5. **Legacy GYD is not currently an executable ERC-20.** Mainnet genesis
   assigns 5B/3B/2B GYD across three addresses. The account model holds these
   balances in a `Tokens` map, but the inspected legacy transaction and storage
   paths do not provide token transfers or persist token balances. The proposed
   migration therefore needs a verified source-of-truth manifest; it must not
   assume there is a mutable, queryable 10B live balance map.
6. **The CometBFT prototype remains isolated.** It uses unique test-only chain
   ID `9198281` and a 2B GYDS test genesis. It now exercises signed native
   transfers and deterministic state execution, but does not implement EVM
   contracts, production fees, or fault testing.
7. **Current wallet setup collects more secret material than this migration
   needs.** The setup page can generate/import a key or recovery phrase and
   posts the private key to the node, which writes it into `.env` with mode
   `0600`. The setup response previously echoed the full `.env`; that response
   leak has been removed and covered by a regression test. Repository search
   found no runtime consumer that uses `GYDS_WALLET_PRIVATE_KEY` to create a
   token or sign a deployment.

This workspace has no verified access to the operator's live mainnet database.
The findings above describe the checked-out code and genesis profiles, not the
current height, balances, or peer health of an external production network.

## Proposed target architecture

### Consensus boundary

- CometBFT should be the only production block-consensus engine if it is later
  approved. Do not run the current `PoSEngine` alongside CometBFT or allow both
  to propose canonical blocks.
- A GYDS application adapter should implement the CometBFT ABCI lifecycle.
  Proposal preparation may order already-valid transactions; proposal
  processing and finalization must independently validate and deterministically
  execute them.
- Extract state transitions from the custom `core.Chain` into deterministic
  application operations. CometBFT must commit an application hash that covers
  the full GYDS state, including native balances, nonces, token state, and EVM
  account/code/storage state once EVM is activated.
- Keep JSON-RPC and dashboard behavior behind adapters that read committed
  application state. Existing HTTP APIs are not evidence that consensus,
  receipts, or historical block queries are compatible with CometBFT.
- Replace custom P2P block propagation with CometBFT peer/consensus networking
  only as part of the coordinated upgrade. Preserve required peer
  authorization, bootstrap, monitoring, and RPC-edge controls.

### Key separation

- Wallet address creation does not require the server to receive a private key.
  An EVM wallet can hold native GYDS and the future GYD ERC-20 at the same
  `0x…` address; the assets have separate balances, not separate account keys.
- Admin access should use the existing wallet challenge/signature flow. The
  browser wallet signs locally; the server verifies the signature.
- CometBFT consensus keys, P2P node keys, admin wallets, and the GYD treasury
  must be separate roles. Do not reuse an operator wallet key as a validator or
  treasury key.
- The current setup import path can send a private key/recovery phrase to the
  server and persist a raw key in `.env`. The key is not needed to deploy an
  ERC-20 from this node. The user chose to retain import but require a prominent
  warning; that warning does not encrypt the key or make server storage safe.
  Do not enter personal or multisig keys.

## Migration stages

### Stage 0 — protocol and governance decisions

Resolve these before production integration:

- Validator admission/removal, validator identities and keys, initial voting
  powers, staking or other eligibility rules, rewards, slashing, missed duties,
  and emergency recovery. CometBFT supplies consensus mechanics; it does not
  decide GYDS economics or validator policy.
- The production CometBFT release/version, consensus parameters, block-time
  targets, peer authorization, and supported upgrade procedure.
- Whether to retain EVM chain ID `198282` across the transition. Preserving it
  is the continuity-oriented default for wallet configuration and EIP-155
  transaction signing, but this needs explicit protocol review.
- A production transaction envelope for legacy native transfers and any
  remaining native operations. EVM transactions must also enforce chain ID,
  nonce, signature recovery, gas, and fork rules. The isolated prototype now
  exercises only EIP-155 legacy native transfers with zero fees; this is not an
  approved production transaction or fee policy.
- An independently reviewed decision for how the legacy chain's final head is
  authorized. The current proposer does not provide cryptographic finality.

### Stage 1 — deterministic application

- Refactor execution so every node processes the same ordered transactions and
  produces the same application hash without relying on wall clock, local
  configuration, map iteration, or untrusted `From` fields.
- Make transaction admission, proposal validation, finalization, receipts,
  indexes, and persisted state use the same rules.
- Include a full state schema/version and explicit migration code. Do not use
  `core/persist.go`'s current account snapshot as a complete GYD/EVM export:
  its account record currently omits token maps and EVM state.
- Preserve a read-only view or documented archive path for legacy blocks and
  transaction queries.

### Stage 2 — isolated testnet and fault testing

- The prototype now validates and executes signed native transfers
  deterministically. Extend it with production-grade pending nonce/fee behavior,
  EVM execution, and the fault/restart/partition scenarios listed below.
- Test multiple independent validators, restart/catch-up, lost peers,
  partitions, invalid signatures/nonces, duplicate transactions, conflicting
  proposals, process crashes, and state restoration from backups.
- Compare block height, committed block identity, validator set, and full
  application hash across nodes after every test.
- Do not connect prototype nodes to mainnet or reuse production data.

### Stage 3 — canonical migration snapshot

- Choose an operator-approved cutover height only after the old network can
  establish an authenticated canonical head.
- Produce a signed, deterministic migration manifest containing chain ID,
  legacy genesis hash, cutover height and block hash, all native balances and
  nonces, the verified legacy GYD allocation/ledger, validator identities, and
  any active EVM state. Verify the manifest independently on every operator's
  node.
- Reconcile GYD exactly to 10B before issuing ERC-20 balances. Current code
  exposes genesis allocations but has no token transfer/persistence path;
  check for any operator-held or external ledger before treating the genesis
  5B/3B/2B allocations as the final distribution.
- Start the CometBFT application at a coordinated height and commit the legacy
  head hash as an explicit migration anchor. Do not claim old and new block
  hashes form one continuous history unless the bridge/anchor and RPC behavior
  are specified and tested.
- Keep the legacy data directory and backups unchanged and read-only. Do not
  overwrite them with CometBFT's block store or silently replay from a different
  genesis.

### Stage 4 — coordinated production upgrade

- Publish and sign one release/upgrade manifest with software version, network
  ID, genesis/anchor hashes, activation height, validator set and powers,
  application state hash, and rollback boundary.
- Require all authorized operators to verify the same manifest and snapshot.
  A normal Git update or installer run must never silently switch consensus.
- Back up the legacy database and validator material securely; validate restore
  before activation. Start all CometBFT validators against the same manifest.
- After CometBFT commits new blocks, do not perform a blind downgrade or reset.
  Recovery must preserve the committed history or be an explicitly governed
  emergency fork.

### Stage 5 — EVM and GYD only after consensus passes

- Select and review EVM fork rules and activation height, then replay the same
  signed EVM transactions on every validator and compare account/storage roots,
  receipts, logs, and gas usage.
- Verify a mainnet-compatible multisig contract address, code, threshold, and
  governance; none is deployed yet.
- Deploy a standard GYD ERC-20 only after the approved migration manifest
  reconciles the old 10B supply. Record the contract address from a verified
  finalized deployment receipt, not a predicted address.

## Production acceptance gates

- Independent protocol/security review approves validator policy, consensus
  parameters, state transition rules, migration anchor, and upgrade/rollback
  procedure.
- At least four independently operated validator nodes pass signed-transaction
  and fault tests with identical committed application hashes. The three-node
  empty-block smoke test is insufficient.
- All nodes recover from process restart and documented backup/restore without
  losing height, finality, balances, nonces, token records, or EVM state.
- The existing chain history is preserved and the migration boundary is
  verifiable by operators and RPC clients.
- EVM activation and GYD ERC-20 deployment are separate gates; passing consensus
  tests alone does not authorize either one.

## Decisions still open

- Production validator identities, key type and custody, voting-power
  distribution, and validator admission/economic policy.
- The approved consensus parameters, CometBFT release, chain ID policy, cutover
  height, migration anchor format, and operator quorum for upgrades.
- The authoritative cutover record for legacy GYD; genesis accounts currently
  total 10B, but no live mainnet data was verified in this workspace.
- Mainnet EVM hardfork/activation schedule and the multisig treasury address.
- How to improve server-side key custody beyond the confirmed warning-only
  setup policy; the current optional import remains unencrypted.
