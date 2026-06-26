package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const fullSetupHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>GYDS Full Node Setup</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0a0e1a;color:#e2e8f0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px}
.card{background:#111827;border:1px solid #1f2937;border-radius:16px;width:100%;max-width:660px;overflow:hidden;box-shadow:0 25px 50px rgba(0,0,0,.5)}
.header{background:linear-gradient(135deg,#7c3aed 0%,#2563eb 100%);padding:28px 32px}
.header h1{font-size:22px;font-weight:700;color:#fff;margin-bottom:4px}
.header p{font-size:13px;color:rgba(255,255,255,.75)}
.badge{display:inline-block;background:rgba(255,255,255,.2);color:#fff;font-size:11px;font-weight:600;padding:2px 10px;border-radius:20px;margin-top:8px;margin-right:6px}
.progress{background:#1f2937;padding:16px 32px;display:flex;gap:0;align-items:center}
.step-dot{width:28px;height:28px;border-radius:50%;background:#1f2937;border:2px solid #374151;display:flex;align-items:center;justify-content:center;font-size:12px;font-weight:700;color:#6b7280;flex-shrink:0;transition:all .3s}
.step-dot.active{background:#7c3aed;border-color:#7c3aed;color:#fff}
.step-dot.done{background:#059669;border-color:#059669;color:#fff}
.step-line{flex:1;height:2px;background:#374151;margin:0 4px}
.step-line.done{background:#059669}
.body{padding:28px 32px}
.step{display:none}.step.active{display:block}
.step-title{font-size:17px;font-weight:700;margin-bottom:4px;color:#f9fafb}
.step-desc{font-size:13px;color:#9ca3af;margin-bottom:20px;line-height:1.5}
.field{margin-bottom:16px}
.field label{display:block;font-size:12px;font-weight:600;color:#9ca3af;text-transform:uppercase;letter-spacing:.05em;margin-bottom:6px}
.field .hint{font-size:11px;color:#6b7280;margin-top:4px;line-height:1.4}
.field input,.field select,.field textarea{width:100%;padding:10px 14px;background:#0f172a;border:1px solid #1f2937;border-radius:8px;color:#e2e8f0;font-size:14px;outline:none;transition:border-color .2s}
.field input:focus,.field select:focus,.field textarea:focus{border-color:#7c3aed}
.field textarea{resize:vertical;min-height:80px;font-family:monospace;font-size:12px}
.field select option{background:#0f172a}
.toggle-row{display:flex;align-items:center;justify-content:space-between;padding:12px 16px;background:#0f172a;border:1px solid #1f2937;border-radius:8px}
.toggle-row span{font-size:13px;color:#d1d5db}
.toggle{position:relative;display:inline-block;width:44px;height:24px}
.toggle input{opacity:0;width:0;height:0}
.slider{position:absolute;cursor:pointer;top:0;left:0;right:0;bottom:0;background:#374151;border-radius:24px;transition:.3s}
.slider:before{position:absolute;content:"";height:18px;width:18px;left:3px;bottom:3px;background:#9ca3af;border-radius:50%;transition:.3s}
input:checked+.slider{background:#7c3aed}
input:checked+.slider:before{transform:translateX(20px);background:#fff}
.info-box{background:#1a103a;border:1px solid #7c3aed;border-radius:8px;padding:14px;margin-bottom:16px}
.info-box p{font-size:12px;color:#c4b5fd;line-height:1.5}
.info-box strong{color:#a78bfa}
.warn-box{background:#1a1000;border:1px solid #d97706;border-radius:8px;padding:14px;margin-bottom:16px}
.warn-box p{font-size:12px;color:#fcd34d;line-height:1.5}
.footer{display:flex;justify-content:space-between;align-items:center;margin-top:24px;gap:12px}
.btn{padding:10px 24px;border-radius:8px;font-size:14px;font-weight:600;cursor:pointer;border:none;transition:all .2s}
.btn-primary{background:#7c3aed;color:#fff}.btn-primary:hover{background:#6d28d9}
.btn-secondary{background:#1f2937;color:#9ca3af;border:1px solid #374151}.btn-secondary:hover{background:#374151;color:#e2e8f0}
.btn:disabled{opacity:.5;cursor:not-allowed}
.review-grid{display:grid;grid-template-columns:1fr 1fr;gap:10px}
.review-item{background:#0f172a;border:1px solid #1f2937;border-radius:8px;padding:12px}
.review-item .key{font-size:11px;color:#6b7280;text-transform:uppercase;letter-spacing:.05em;margin-bottom:4px}
.review-item .val{font-size:13px;color:#e2e8f0;word-break:break-all}
.env-box{background:#0f172a;border:1px solid #1f2937;border-radius:8px;padding:16px;margin-top:16px}
.env-box pre{font-family:monospace;font-size:12px;color:#86efac;line-height:1.8;white-space:pre-wrap;word-break:break-all}
.success-icon{text-align:center;padding:16px 0;font-size:48px}
.cmd-box{background:#0f172a;border:1px solid #374151;border-radius:8px;padding:12px 16px;margin-top:10px;font-family:monospace;font-size:12px;color:#7dd3fc;cursor:pointer;position:relative}
.cmd-box:hover{border-color:#7c3aed}
.copy-hint{position:absolute;right:10px;top:50%;transform:translateY(-50%);font-size:10px;color:#4b5563}
.tag{display:inline-block;background:#7c3aed;color:#fff;font-size:10px;font-weight:700;padding:2px 8px;border-radius:4px;margin-left:6px;vertical-align:middle}
.tag.optional{background:#374151;color:#9ca3af}
.two-col{display:grid;grid-template-columns:1fr 1fr;gap:12px}
.step-label{font-size:10px;text-align:center;color:#6b7280;margin-top:4px}
</style>
</head>
<body>
<div class="card">
  <div class="header">
    <h1>🖥️ GYDS Full Node Setup</h1>
    <p>Full blockchain node — stores complete state, serves RPC, participates in P2P</p>
    <span class="badge">Chain ID: 13370</span>
    <span class="badge">netlifegy.com</span>
  </div>
  <div class="progress" id="progress">
    <div class="step-dot active" id="dot-1">1</div>
    <div class="step-line" id="line-1"></div>
    <div class="step-dot" id="dot-2">2</div>
    <div class="step-line" id="line-2"></div>
    <div class="step-dot" id="dot-3">3</div>
    <div class="step-line" id="line-3"></div>
    <div class="step-dot" id="dot-4">4</div>
    <div class="step-line" id="line-4"></div>
    <div class="step-dot" id="dot-5">5</div>
    <div class="step-line" id="line-5"></div>
    <div class="step-dot" id="dot-6">6</div>
  </div>
  <div class="body">

    <!-- Step 1: Welcome -->
    <div class="step active" id="step-1">
      <div class="step-title">Welcome to GYDS Full Node</div>
      <div class="step-desc">This wizard will configure your full node. A full node stores the complete blockchain state, validates all transactions, and serves the JSON-RPC API.</div>
      <div class="info-box">
        <p>✅ <strong>What a Full Node does:</strong> Maintains complete chain state, validates every block and transaction, participates in P2P gossip, and serves the RPC/WebSocket API.</p>
        <br><p>⚠️ <strong>Requirements:</strong> 4GB+ RAM, 100GB+ SSD, stable internet, open TCP ports for P2P.</p>
      </div>
      <div class="review-grid">
        <div class="review-item"><div class="key">Node Type</div><div class="val">Full (Complete State)</div></div>
        <div class="review-item"><div class="key">Chain ID</div><div class="val">13370</div></div>
        <div class="review-item"><div class="key">Block Time</div><div class="val">120 seconds</div></div>
        <div class="review-item"><div class="key">Consensus</div><div class="val">Proof of Stake (PoS)</div></div>
        <div class="review-item"><div class="key">Min RAM</div><div class="val">4 GB</div></div>
        <div class="review-item"><div class="key">Min Disk</div><div class="val">100 GB SSD</div></div>
      </div>
      <div class="footer"><span></span><button class="btn btn-primary" onclick="go(2)">Start Setup →</button></div>
    </div>

    <!-- Step 2: Network Ports -->
    <div class="step" id="step-2">
      <div class="step-title">Network &amp; Ports</div>
      <div class="step-desc">Configure the RPC, WebSocket, and P2P ports. These must be open in your firewall.</div>
      <div class="two-col">
        <div class="field">
          <label>RPC Port <span class="tag">Required</span></label>
          <input type="number" id="rpc_port" value="8545" min="1024" max="65535">
          <div class="hint">JSON-RPC HTTP port. Default: 8545</div>
        </div>
        <div class="field">
          <label>WebSocket Port <span class="tag">Required</span></label>
          <input type="number" id="ws_port" value="8546" min="1024" max="65535">
          <div class="hint">WS subscriptions port. Default: 8546</div>
        </div>
      </div>
      <div class="two-col">
        <div class="field">
          <label>P2P Port <span class="tag">Required</span></label>
          <input type="number" id="p2p_port" value="30303" min="1024" max="65535">
          <div class="hint">Peer discovery &amp; sync. Default: 30303</div>
        </div>
        <div class="field">
          <label>Max Peers</label>
          <input type="number" id="max_peers" value="25" min="1" max="200">
          <div class="hint">Max simultaneous P2P peers. Default: 25</div>
        </div>
      </div>
      <div class="field">
        <label>RPC Host Binding</label>
        <select id="rpc_host">
          <option value="0.0.0.0" selected>0.0.0.0 — all interfaces (public RPC)</option>
          <option value="127.0.0.1">127.0.0.1 — localhost only (private)</option>
        </select>
        <div class="hint">Use 0.0.0.0 if you want to expose the RPC to the network/internet (required for wallets).</div>
      </div>
      <div class="footer">
        <button class="btn btn-secondary" onclick="go(1)">← Back</button>
        <button class="btn btn-primary" onclick="go(3)">Next →</button>
      </div>
    </div>

    <!-- Step 3: Bootstrap Peers -->
    <div class="step" id="step-3">
      <div class="step-title">Bootstrap Peers</div>
      <div class="step-desc">Bootstrap nodes help your node discover and connect to the GYDS network on first start.</div>
      <div class="field">
        <label>Bootstrap Node Addresses <span class="tag optional">Optional</span></label>
        <textarea id="bootstrap_nodes" placeholder="/ip4/1.2.3.4/tcp/30303/p2p/16Uiu2...&#10;enode://abc123...@1.2.3.4:30303"></textarea>
        <div class="hint">One per line. Leave blank to use only local discovery. Ask your network operator for bootstrap addresses.</div>
      </div>
      <div class="info-box">
        <p>💡 <strong>Tip:</strong> If you are joining the GYDS mainnet, contact the network team at <strong>netlifegy.com</strong> for the latest bootstrap node list. Without bootstrap nodes your node will only discover peers via local mDNS.</p>
      </div>
      <div class="footer">
        <button class="btn btn-secondary" onclick="go(2)">← Back</button>
        <button class="btn btn-primary" onclick="go(4)">Next →</button>
      </div>
    </div>

    <!-- Step 4: Storage & Logging -->
    <div class="step" id="step-4">
      <div class="step-title">Storage &amp; Logging</div>
      <div class="step-desc">Choose where blockchain data is stored and set log verbosity.</div>
      <div class="field">
        <label>Data Directory <span class="tag">Required</span></label>
        <input type="text" id="data_dir" value="/var/lib/gyds-fullnode" placeholder="/var/lib/gyds-fullnode">
        <div class="hint">Full path where blockchain state.db, keystore, and logs are stored. Needs 100GB+ free space.</div>
      </div>
      <div class="field">
        <label>Log Level</label>
        <select id="log_level">
          <option value="info" selected>info (recommended)</option>
          <option value="debug">debug (verbose — for development)</option>
          <option value="warn">warn (quieter)</option>
          <option value="error">error (errors only)</option>
        </select>
      </div>
      <div class="field">
        <label>Log Format</label>
        <select id="log_format">
          <option value="json" selected>json (for log collectors)</option>
          <option value="pretty">pretty (human readable)</option>
        </select>
      </div>
      <div class="footer">
        <button class="btn btn-secondary" onclick="go(3)">← Back</button>
        <button class="btn btn-primary" onclick="go(5)">Next →</button>
      </div>
    </div>

    <!-- Step 5: Domain / Nginx -->
    <div class="step" id="step-5">
      <div class="step-title">Domain &amp; Reverse Proxy</div>
      <div class="step-desc">Optionally set a domain name for your node. The installer can configure Nginx + SSL automatically.</div>
      <div class="field">
        <label>Domain Name <span class="tag optional">Optional</span></label>
        <input type="text" id="domain" placeholder="rpc.yournode.com">
        <div class="hint">Leave blank to use IP-only access. If set, Nginx will proxy RPC/WS on port 80/443 and certbot will issue a TLS certificate.</div>
      </div>
      <div class="field">
        <label>Enable Snapshot Sync</label>
        <div class="toggle-row">
          <span>Download a chain snapshot for faster initial sync</span>
          <label class="toggle"><input type="checkbox" id="snapshot_sync" checked><span class="slider"></span></label>
        </div>
        <div class="hint">Recommended for new nodes — syncs from a trusted snapshot instead of block-by-block from genesis.</div>
      </div>
      <div class="warn-box">
        <p>⚠️ <strong>Firewall reminder:</strong> Make sure TCP ports <strong id="port-reminder">8545, 8546, 30303</strong> are open in your firewall and security group before starting the node.</p>
      </div>
      <div class="footer">
        <button class="btn btn-secondary" onclick="go(4)">← Back</button>
        <button class="btn btn-primary" onclick="go(6)">Review →</button>
      </div>
    </div>

    <!-- Step 6: Review & Save -->
    <div class="step" id="step-6">
      <div class="step-title">Review &amp; Save Configuration</div>
      <div class="step-desc">Check all settings before applying. Click "Save &amp; Apply" to write the config file.</div>
      <div id="review-content"></div>
      <div class="env-box"><pre id="env-preview"></pre></div>
      <div class="footer">
        <button class="btn btn-secondary" onclick="go(5)">← Back</button>
        <button class="btn btn-primary" id="save-btn" onclick="saveConfig()">💾 Save &amp; Apply</button>
      </div>
    </div>

    <!-- Step 7: Done -->
    <div class="step" id="step-7">
      <div class="success-icon">✅</div>
      <div class="step-title" style="text-align:center">Configuration Saved!</div>
      <div class="step-desc" style="text-align:center;margin-bottom:20px">Your full node config has been written. Use the commands below to start your node.</div>
      <div id="done-content"></div>
      <div class="footer" style="justify-content:center;margin-top:24px">
        <button class="btn btn-primary" onclick="window.location.reload()">🔄 Reconfigure</button>
      </div>
    </div>

  </div>
</div>
<script>
let cur=1;
const STEPS=6;
function go(n){
  if(n===6) buildReview();
  updatePortReminder();
  document.getElementById('step-'+cur).classList.remove('active');
  const cd=document.getElementById('dot-'+cur);
  if(cd){cd.classList.remove('active');if(cur<n)cd.classList.add('done');}
  for(let i=1;i<=STEPS-1;i++){
    const l=document.getElementById('line-'+i);
    if(l)l.classList.toggle('done',i<n);
  }
  cur=n;
  const nd=document.getElementById('dot-'+cur);
  if(nd){nd.classList.remove('done');nd.classList.add('active');}
  const ns=document.getElementById('step-'+cur);
  if(ns)ns.classList.add('active');
}
function gv(id){return(document.getElementById(id)||{}).value||'';}
function gc(id){return document.getElementById(id)?.checked;}
function updatePortReminder(){
  const r=document.getElementById('port-reminder');
  if(r)r.textContent=gv('rpc_port')+', '+gv('ws_port')+', '+gv('p2p_port');
}
function buildReview(){
  const bn=gv('bootstrap_nodes').split('\n').filter(x=>x.trim());
  const env=[
    '# GYDS Full Node Configuration',
    '# Generated: '+new Date().toISOString(),
    '',
    'GYDS_CHAIN_ID=13370',
    'GYDS_NODE_MODE=full',
    'GYDS_RPC_PORT='+gv('rpc_port'),
    'GYDS_RPC_HOST='+gv('rpc_host'),
    'GYDS_WS_PORT='+gv('ws_port'),
    'GYDS_P2P_PORT='+gv('p2p_port'),
    'GYDS_MAX_PEERS='+gv('max_peers'),
    bn.length?'GYDS_BOOTSTRAP_NODES='+bn.join(','):'# GYDS_BOOTSTRAP_NODES= (none)',
    'GYDS_DATA_DIR='+gv('data_dir'),
    'GYDS_LOG_LEVEL='+gv('log_level'),
    'GYDS_LOG_FORMAT='+gv('log_format'),
    gv('domain')?'DOMAIN='+gv('domain'):'# DOMAIN= (not set)',
    'GYDS_SNAPSHOT_SYNC='+(gc('snapshot_sync')?'true':'false'),
  ].join('\n');
  document.getElementById('env-preview').textContent=env;
  const vals=[
    ['RPC Port',gv('rpc_port')],['WS Port',gv('ws_port')],['P2P Port',gv('p2p_port')],
    ['Max Peers',gv('max_peers')],['RPC Host',gv('rpc_host')],['Data Dir',gv('data_dir')],
    ['Log Level',gv('log_level')],['Log Format',gv('log_format')],
    ['Domain',gv('domain')||'(none — IP only)'],['Snapshot Sync',gc('snapshot_sync')?'Yes':'No'],
    ['Bootstrap Nodes',gv('bootstrap_nodes').trim()||'(none)'],
  ];
  document.getElementById('review-content').innerHTML='<div class="review-grid">'+vals.map(([k,v])=>'<div class="review-item"><div class="key">'+k+'</div><div class="val">'+v+'</div></div>').join('')+'</div>';
}
async function saveConfig(){
  const btn=document.getElementById('save-btn');
  btn.disabled=true; btn.textContent='Saving...';
  const bn=gv('bootstrap_nodes').split('\n').filter(x=>x.trim());
  const body={
    rpc_port:parseInt(gv('rpc_port'))||8545, ws_port:parseInt(gv('ws_port'))||8546,
    p2p_port:parseInt(gv('p2p_port'))||30303, max_peers:parseInt(gv('max_peers'))||25,
    rpc_host:gv('rpc_host'), bootstrap_nodes:bn,
    data_dir:gv('data_dir'), log_level:gv('log_level'), log_format:gv('log_format'),
    domain:gv('domain'), snapshot_sync:gc('snapshot_sync'),
  };
  const r=await fetch('/api/configure',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const data=await r.json();
  if(!r.ok){alert('Error: '+data.error); btn.disabled=false; btn.textContent='Save & Apply'; return;}
  const fwPorts=gv('rpc_port')+'/tcp, '+gv('ws_port')+'/tcp, '+gv('p2p_port')+'/tcp/udp';
  document.getElementById('done-content').innerHTML=
    '<div class="info-box"><p>Config written to: <strong>'+data.config_file+'</strong></p></div>'+
    '<p style="font-size:13px;color:#9ca3af;margin:12px 0 6px">Start the node:</p>'+
    '<div class="cmd-box" onclick="cp(this)"><span class="copy-hint">click to copy</span>'+data.start_cmd+'</div>'+
    (data.service_cmd?'<p style="font-size:13px;color:#9ca3af;margin:12px 0 6px">Or via systemd:</p><div class="cmd-box" onclick="cp(this)"><span class="copy-hint">click to copy</span>'+data.service_cmd+'</div>':'')+
    '<div class="warn-box" style="margin-top:16px"><p>🔓 Open firewall ports: <strong>'+fwPorts+'</strong></p></div>'+
    '<p style="font-size:13px;color:#9ca3af;margin:12px 0 6px">Add to MetaMask / wallets:</p>'+
    '<div class="cmd-box"><strong>Network Name:</strong> GYDS Chain &nbsp;|&nbsp; <strong>Chain ID:</strong> 13370 &nbsp;|&nbsp; <strong>RPC URL:</strong> '+(data.domain?'https://'+data.domain:'http://YOUR_SERVER_IP:'+gv('rpc_port'))+'</div>';
  document.getElementById('step-6').classList.remove('active');
  document.getElementById('step-7').classList.add('active');
}
function cp(el){const t=el.querySelector('span.copy-hint');const txt=el.textContent.replace('click to copy','').trim();navigator.clipboard.writeText(txt).then(()=>{if(t){const o=t.textContent;t.textContent='copied!';setTimeout(()=>t.textContent=o,1500);}});}
</script>
</body>
</html>`

// FullSetupServer serves the fullnode setup wizard
type FullSetupServer struct {
	version string
}

type fullSetupRequest struct {
	RPCPort        int      `json:"rpc_port"`
	WSPort         int      `json:"ws_port"`
	P2PPort        int      `json:"p2p_port"`
	MaxPeers       int      `json:"max_peers"`
	RPCHost        string   `json:"rpc_host"`
	BootstrapNodes []string `json:"bootstrap_nodes"`
	DataDir        string   `json:"data_dir"`
	LogLevel       string   `json:"log_level"`
	LogFormat      string   `json:"log_format"`
	Domain         string   `json:"domain"`
	SnapshotSync   bool     `json:"snapshot_sync"`
}

// startFullSetupWizard launches the embedded setup web server for the fullnode
func startFullSetupWizard(wizardPort int, nodeVersion string) {
	srv := &FullSetupServer{version: nodeVersion}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/api/configure", srv.handleConfigure)

	localIP := getFullLocalIP()
	url := fmt.Sprintf("http://localhost:%d", wizardPort)
	localURL := fmt.Sprintf("http://%s:%d", localIP, wizardPort)

	fmt.Printf("\n")
	fmt.Printf("╔══════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║   GYDS Full Node — Web Setup Wizard                         ║\n")
	fmt.Printf("╠══════════════════════════════════════════════════════════════╣\n")
	fmt.Printf("║                                                              ║\n")
	fmt.Printf("║  Open this URL in your browser to configure the node:       ║\n")
	fmt.Printf("║                                                              ║\n")
	fmt.Printf("║  ➜  %-55s ║\n", url)
	fmt.Printf("║  ➜  %-55s ║\n", localURL+" (network)")
	fmt.Printf("║                                                              ║\n")
	fmt.Printf("║  Press Ctrl+C to stop the wizard server.                    ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════════╝\n\n")

	go tryFullOpenBrowser(url)

	server := &http.Server{Addr: fmt.Sprintf(":%d", wizardPort), Handler: mux}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "Setup server error: %v\n", err)
		os.Exit(1)
	}
}

func (s *FullSetupServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	tmpl, _ := template.New("setup").Parse(fullSetupHTML)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, nil)
}

func (s *FullSetupServer) handleConfigure(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}

	var req fullSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Defaults
	if req.RPCPort == 0 { req.RPCPort = 8545 }
	if req.WSPort == 0 { req.WSPort = 8546 }
	if req.P2PPort == 0 { req.P2PPort = 30303 }
	if req.MaxPeers == 0 { req.MaxPeers = 25 }
	if req.RPCHost == "" { req.RPCHost = "0.0.0.0" }
	if req.DataDir == "" { req.DataDir = "/var/lib/gyds-fullnode" }
	if req.LogLevel == "" { req.LogLevel = "info" }
	if req.LogFormat == "" { req.LogFormat = "json" }

	dataDir := req.DataDir

	lines := []string{
		"# GYDS Full Node Configuration",
		"# Generated by setup wizard on " + time.Now().Format("2006-01-02 15:04:05"),
		"",
		"GYDS_CHAIN_ID=13370",
		"GYDS_NODE_MODE=full",
		fmt.Sprintf("GYDS_RPC_PORT=%d", req.RPCPort),
		"GYDS_RPC_HOST=" + req.RPCHost,
		fmt.Sprintf("GYDS_WS_PORT=%d", req.WSPort),
		fmt.Sprintf("GYDS_P2P_PORT=%d", req.P2PPort),
		fmt.Sprintf("GYDS_MAX_PEERS=%d", req.MaxPeers),
		"GYDS_DATA_DIR=" + dataDir,
		"GYDS_LOG_LEVEL=" + req.LogLevel,
		"GYDS_LOG_FORMAT=" + req.LogFormat,
		fmt.Sprintf("GYDS_SNAPSHOT_SYNC=%v", req.SnapshotSync),
	}
	if len(req.BootstrapNodes) > 0 {
		lines = append(lines, "GYDS_BOOTSTRAP_NODES="+strings.Join(req.BootstrapNodes, ","))
	}
	if req.Domain != "" {
		lines = append(lines, "DOMAIN="+req.Domain)
	}
	envContent := strings.Join(lines, "\n") + "\n"

	// Write config directory
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		// Try fallback in home dir
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".gyds-fullnode", "config")
		dataDir = filepath.Join(home, ".gyds-fullnode")
		_ = os.MkdirAll(configDir, 0700)
	}

	configFile := filepath.Join(configDir, "node.env")
	if err := os.WriteFile(configFile, []byte(envContent), 0600); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "cannot write config: " + err.Error()})
		return
	}

	exePath, _ := os.Executable()
	if exePath == "" {
		exePath = "gyds-fullnode"
	}

	startCmd := fmt.Sprintf("set -a && source %s && set +a && %s start", configFile, exePath)
	serviceCmd := ""
	if _, err := os.Stat("/etc/systemd/system"); err == nil {
		serviceCmd = "systemctl start gyds-fullnode   # requires root — use installer to set up service"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":          true,
		"config_file": configFile,
		"start_cmd":   startCmd,
		"service_cmd": serviceCmd,
		"domain":      req.Domain,
		"env":         envContent,
	})
}

func getFullLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "localhost"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func tryFullOpenBrowser(url string) {
	time.Sleep(800 * time.Millisecond)
	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("xdg-open", url).Start()
	case "darwin":
		_ = exec.Command("open", url).Start()
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
}
