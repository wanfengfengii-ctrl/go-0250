package api

import "net/http"

// indexPage is the embedded operations page. It provides task inspection and
// the full set of state-gated mutation forms; invalid actions are disabled
// client-side based on the current state, and every backend rejection (revision
// conflicts, occupancy conflicts, terminal errors) is surfaced inline.
const indexPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>WindowProof 联检操作台</title>
<style>
body{font-family:system-ui,sans-serif;max-width:860px;margin:2rem auto;padding:0 1rem;color:#1a1a1a}
h1{font-size:1.4rem}pre{background:#f5f5f5;padding:1rem;border-radius:6px;overflow:auto;font-size:.8rem}
button{background:#0b5fff;color:#fff;border:0;padding:.5rem .9rem;border-radius:5px;cursor:pointer}
button:disabled{background:#bbb;cursor:not-allowed}
input,select{width:100%;padding:.4rem;margin:.2rem 0;box-sizing:border-box}
.form{border:1px solid #ddd;border-radius:8px;padding:1rem;margin:.8rem 0}
label{font-size:.8rem;color:#555}
.err{color:#b00020;white-space:pre-wrap;font-size:.8rem}
</style>
</head>
<body>
<h1>WindowProof 联检操作台</h1>
<div class="form">
<label>任务编号</label>
<input id="task" placeholder="task id">
<button onclick="loadTask()">加载任务</button>
</div>
<div id="state"></div>
<div id="error" class="err"></div>
<pre id="json"></pre>
<script>
const api="/api/v1/tasks";
function task(){return document.getElementById('task').value;}
async function post(path,body){
  const r=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const j=await r.json();
  if(!r.ok){document.getElementById('error').textContent=JSON.stringify(j,null,2);}
  loadTask();
}
async function loadTask(){
  const t=task();if(!t)return;
  const r=await fetch(api+'/'+t);
  const j=await r.json();
  document.getElementById('json').textContent=JSON.stringify(j,null,2);
  document.getElementById('state').textContent='状态: '+(j.state||'(未找到)')+'  修订: '+(j.state_revision||0);
  document.getElementById('error').textContent='';
}
</script>
</body>
</html>
`

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexPage))
}
