package setup


const HtmlPage = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Strct Wifi Setup</title>
    <style>
        :root {
            --bg-color: #e3e1db;
            --bg-gradient-start: #ebe9e4;
            --bg-gradient-end: #d6d4ce;
            --text-main: #1d1d1f;
            --text-sub: #555;
            --accent-yellow: #ffc233;
            --accent-hover: #ecc04d;
            --card-bg: rgba(240, 239, 237, 0.8);
            --border-color: rgba(255, 255, 255, 0.4);
        }

        * { box-sizing: border-box; margin: 0; padding: 0; }

        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            background-color: var(--bg-color);
            background: linear-gradient(135deg, var(--bg-gradient-start), var(--bg-gradient-end));
            color: var(--text-main);
            min-height: 100vh;
            display: flex;
            flex-direction: column;
            overflow-x: hidden;
        }

        /* Animations */
        @keyframes fadeInUp {
            from { opacity: 0; transform: translateY(30px); }
            to { opacity: 1; transform: translateY(0); }
        }

        .animate-in {
            animation: fadeInUp 0.8s cubic-bezier(0.25, 0.1, 0.25, 1) forwards;
            opacity: 0;
        }

        .delay-1 { animation-delay: 0.1s; }
        .delay-2 { animation-delay: 0.2s; }
        .delay-3 { animation-delay: 0.3s; }

        /* Layout */
        .container {
            max-width: 1200px;
            margin: 0 auto;
            padding: 40px 24px;
            flex: 1;
            display: flex;
            flex-direction: column;
            justify-content: center;
        }

        .header-section {
            text-align: center;
            margin-bottom: 40px;
        }

        h1 {
            font-size: 3.5rem;
            font-weight: 700;
            line-height: 1.1;
            letter-spacing: -0.02em;
            margin-bottom: 20px;
            color: #24292f;
        }

        .subtitle {
            font-size: 1.5rem;
            color: var(--text-sub);
            font-weight: 500;
            display: block;
            margin-bottom: 10px;
        }

        /* Action Area */
        .action-area {
            display: flex;
            flex-direction: column;
            align-items: center;
            gap: 20px;
            width: 100%;
            max-width: 500px;
            margin: 0 auto;
        }

        .card {
            background: var(--card-bg);
            backdrop-filter: blur(12px);
            -webkit-backdrop-filter: blur(12px);
            border: 1px solid var(--border-color);
            border-radius: 20px;
            padding: 30px;
            width: 100%;
            box-shadow: 0 10px 30px rgba(0,0,0,0.05);
            transition: transform 0.3s ease;
        }

        /* Buttons */
        .btn {
            background-color: var(--accent-yellow);
            color: var(--text-main);
            padding: 16px 40px;
            border-radius: 9999px;
            font-weight: 600;
            font-size: 17px;
            border: none;
            cursor: pointer;
            transition: background 0.2s ease, transform 0.1s ease;
            box-shadow: 0 2px 5px rgba(0,0,0,0.05);
            display: inline-block;
            text-decoration: none;
        }

        .btn:hover { background-color: var(--accent-hover); }
        .btn:active { transform: scale(0.98); }

        .btn-secondary {
            background: transparent;
            border: 1px solid #888;
            color: var(--text-main);
        }
        .btn-secondary:hover { background: rgba(0,0,0,0.05); }

        /* Network List */
        #list {
            list-style: none;
            margin-top: 20px;
            max-height: 300px;
            overflow-y: auto;
            border-radius: 12px;
        }

        .net-item {
            background: rgba(255,255,255,0.6);
            padding: 15px 20px;
            margin-bottom: 8px;
            border-radius: 12px;
            cursor: pointer;
            display: flex;
            justify-content: space-between;
            align-items: center;
            transition: background 0.2s;
        }

        .net-item:hover { background: white; }
        .net-item strong { display: block; font-size: 16px; }
        .net-item small { color: #666; font-size: 12px; }

        /* Form */
        input {
            width: 100%;
            padding: 16px;
            border-radius: 12px;
            border: 1px solid #ccc;
            margin-bottom: 15px;
            font-size: 16px;
            background: rgba(255,255,255,0.9);
            outline: none;
        }
        input:focus { border-color: var(--accent-yellow); box-shadow: 0 0 0 3px rgba(255,194,51,0.3); }

        /* Utils */
        .hidden { display: none !important; }
        .spinner {
            width: 24px; height: 24px;
            border: 3px solid rgba(0,0,0,0.1);
            border-radius: 50%;
            border-top-color: var(--text-main);
            animation: spin 1s linear infinite;
            margin: 0 auto;
        }
        @keyframes spin { 100% { transform: rotate(360deg); } }

        /* Responsive */
        @media (max-width: 768px) {
            h1 { font-size: 2.5rem; }
            .container { padding: 20px; }
        }
    </style>
</head>
<body>

    <div class="container">
        <div class="header-section animate-in">
            <span class="subtitle">StructIO Agent</span>
            <h1>Connect your device<br>to the cloud</h1>
        </div>

        <div class="action-area animate-in delay-1">
            
            <!-- Initial State -->
            <div id="intro-card" class="card">
                <p style="margin-bottom: 20px; color: #444; font-size: 18px; text-align: center;">
                    Configure Wi-Fi to bring your node online.
                </p>
                <button class="btn" onclick="scan()" style="width: 100%">Find Networks</button>
            </div>

            <!-- Loading State -->
            <div id="loading" class="hidden" style="text-align: center;">
                <div class="spinner"></div>
                <p style="margin-top: 10px; color: #666;">Scanning for networks...</p>
            </div>

            <!-- List State -->
            <div id="list-card" class="card hidden">
                <h3 style="margin-bottom: 10px;">Select Network</h3>
                <ul id="list"></ul>
                <button class="btn-secondary" onclick="resetUI()" style="width:100%; margin-top:10px; padding: 10px; border-radius: 10px;">Cancel</button>
            </div>

            <!-- Form State -->
            <div id="form-card" class="card hidden">
                <h3 style="margin-bottom: 5px;" id="selected-ssid"></h3>
                <p style="margin-bottom: 15px; font-size: 14px; color: #666;">Enter password to connect</p>
                
                <input id="ssid-hidden" type="hidden">
                <input id="pass" type="password" placeholder="Password">
                
                <button class="btn" onclick="connect()" style="width: 100%">Connect</button>
                <button class="btn-secondary" onclick="backToList()" style="width: 100%; margin-top: 10px; padding: 12px; border-radius: 9999px; border:none; color: #666;">Back</button>
            </div>

             <!-- Success State -->
             <div id="success-card" class="card hidden" style="text-align: center">
                <h3 style="margin-bottom: 10px;">Connecting...</h3>
                <p>The device is restarting its network interface.<br>You can close this page.</p>
            </div>

        </div>
    </div>

<script>
    const el = (id) => document.getElementById(id);

    function show(id) {
        ['intro-card', 'loading', 'list-card', 'form-card', 'success-card'].forEach(i => el(i).classList.add('hidden'));
        el(id).classList.remove('hidden');
    }

    async function scan() {
        show('loading');
        try {
            let res = await fetch('/scan');
            if (!res.ok) throw new Error("Scan failed");
            let nets = await res.json();
            
            let list = el('list');
            list.innerHTML = '';
            
            if (nets.length === 0) {
                list.innerHTML = '<li style="text-align:center; padding:10px;">No networks found</li>';
            }

            nets.forEach(n => {
                let li = document.createElement('li');
                li.className = 'net-item';
                li.innerHTML = '<div><strong>' + n.SSID + '</strong><small>' + n.Security + '</small></div><span>' + n.Signal + '%</span>';
                li.onclick = () => {
                    el('ssid-hidden').value = n.SSID;
                    el('selected-ssid').innerText = n.SSID;
                    el('pass').value = '';
                    show('form-card');
                };
                list.appendChild(li);
            });
            show('list-card');
        } catch (e) {
            alert("Error scanning networks");
            resetUI();
        }
    }

    async function connect() {
        let ssid = el('ssid-hidden').value;
        let pass = el('pass').value;
        
        let btn = document.querySelector('#form-card .btn');
        btn.innerText = "Verifying...";
        btn.disabled = true;

        try {
            let res = await fetch('/connect', {
                method: 'POST',
                body: JSON.stringify({ssid, password: pass})
            });
            if (!res.ok) throw new Error("Connection failed");
            show('success-card');
        } catch (e) {
            alert("Failed to send credentials.");
            btn.innerText = "Connect";
            btn.disabled = false;
        }
    }

    function resetUI() { show('intro-card'); }
    function backToList() { show('list-card'); }
</script>
</body>
</html>
`
