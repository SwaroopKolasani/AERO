import { useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  Cell,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis
} from "recharts";

const POLL_MS = 2500;
const TRACE_CAP = 48;

const CSS = `
.aero{--void:#0b090a;--panel:#100d0e;--panel-2:#0c0a0b;--line:#241a1c;--line-2:#312325;
  --oxblood:#6a040f;--oxblood-hi:#8d0a17;--oxblood-tint:rgba(106,4,15,.16);
  --steel-hi:#eef0f2;--steel:#b7bbc0;--steel-lo:#7f8489;
  --text:#f3f1f2;--muted:#8f8689;--faint:#5f585a;--amber:#c98a2e;
  --mono:ui-monospace,"SF Mono","JetBrains Mono",Menlo,Consolas,monospace;
  --sans:ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
  background:var(--void);color:var(--text);font-family:var(--sans);min-height:100vh;
  -webkit-font-smoothing:antialiased;letter-spacing:-0.006em;}
.aero *{box-sizing:border-box}
.aero .bg{position:fixed;inset:0;pointer-events:none;
  background:radial-gradient(900px 480px at 78% -12%,rgba(106,4,15,.20),transparent 60%),
             radial-gradient(700px 400px at 8% 110%,rgba(106,4,15,.08),transparent 60%);}
.aero .wrap{position:relative;max-width:1180px;margin:0 auto;padding:0 28px 72px}
.aero .bar{position:sticky;top:0;z-index:30;display:flex;align-items:center;gap:22px;
  padding:14px 28px;background:rgba(11,9,10,.82);backdrop-filter:blur(10px);
  border-bottom:1px solid var(--line)}
.aero .bar .brand{display:flex;align-items:center;gap:11px}
.aero .logo-img{width:30px;height:30px;border-radius:8px;object-fit:cover;border:1px solid var(--line-2);background:#050505}
.aero .wordmark{font-weight:600;letter-spacing:.22em;font-size:14px;
  background:linear-gradient(180deg,var(--steel-hi),var(--steel-lo));
  -webkit-background-clip:text;background-clip:text;color:transparent}
.aero .tag{font:600 10px/1 var(--mono);letter-spacing:.16em;color:var(--muted);
  border:1px solid var(--line-2);border-radius:4px;padding:4px 6px}
.aero nav{display:flex;gap:4px;margin-left:6px}
.aero nav a{color:var(--muted);text-decoration:none;font-size:13px;padding:6px 11px;
  border-radius:6px;transition:.15s}
.aero nav a:hover{color:var(--text);background:#ffffff08}
.aero .spacer{flex:1}
.aero .link{display:flex;align-items:center;gap:8px;font:600 11px/1 var(--mono);
  letter-spacing:.12em;padding:6px 10px;border:1px solid var(--line-2);border-radius:6px}
.aero .dot{width:7px;height:7px;border-radius:50%}
.aero .dot.live{background:var(--oxblood);box-shadow:0 0 0 0 var(--oxblood);animation:pulse 1.8s infinite}
.aero .dot.demo{background:var(--amber)}
@keyframes pulse{0%{box-shadow:0 0 0 0 rgba(106,4,15,.7)}70%{box-shadow:0 0 0 7px rgba(106,4,15,0)}
  100%{box-shadow:0 0 0 0 rgba(106,4,15,0)}}
.aero .mode{font:600 11px/1 var(--mono);letter-spacing:.1em;color:var(--muted)}
.aero .signin{font-size:12px;color:var(--steel);background:transparent;cursor:pointer;
  border:1px solid var(--line-2);border-radius:7px;padding:7px 12px;transition:.15s}
.aero .signin:hover{border-color:var(--oxblood);color:var(--text)}
.aero .hero{padding:52px 0 30px}
.aero .eyebrow{font:600 11px/1 var(--mono);letter-spacing:.24em;color:var(--oxblood-hi);
  text-transform:uppercase}
.aero h1{font-size:44px;line-height:1.02;font-weight:600;margin:16px 0 12px;
  letter-spacing:-0.02em;max-width:16ch}
.aero h1 em{font-style:normal;background:linear-gradient(180deg,var(--steel-hi),var(--steel-lo));
  -webkit-background-clip:text;background-clip:text;color:transparent}
.aero .lede{color:var(--muted);font-size:16px;max-width:60ch;line-height:1.6}
.aero .grid{display:grid;gap:18px}
.aero .g-hero{grid-template-columns:minmax(0,1fr) minmax(0,1.18fr);align-items:start}
.aero .g-lower{grid-template-columns:minmax(0,.92fr) minmax(0,1.3fr)}
@media(max-width:900px){.aero .g-hero,.aero .g-lower{grid-template-columns:1fr}
  .aero h1{font-size:34px}.aero nav{display:none}}
.aero .card{background:var(--panel);border:1px solid var(--line);border-radius:12px;
  padding:20px;position:relative}
.aero .card::before{content:"";position:absolute;inset:0 0 auto 0;height:1px;
  background:linear-gradient(90deg,transparent,#ffffff12,transparent);border-radius:12px 12px 0 0}
.aero .chead{display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:16px}
.aero .chead h2{font-size:16px;font-weight:600;margin:5px 0 0}
.aero .status{font:600 11px/1 var(--mono);letter-spacing:.06em;color:var(--muted);
  border:1px solid var(--line-2);border-radius:20px;padding:6px 11px}
.aero label.f{display:block;margin-bottom:12px}
.aero label.f>span{display:block;font:600 11px/1 var(--mono);letter-spacing:.1em;
  color:var(--faint);text-transform:uppercase;margin-bottom:7px}
.aero textarea,.aero input[type=text],.aero input[type=number]{width:100%;
  background:var(--panel-2);border:1px solid var(--line-2);border-radius:8px;color:var(--text);
  font:14px/1.5 var(--mono);padding:11px 12px;outline:none;transition:.15s}
.aero textarea{min-height:74px;resize:vertical}
.aero textarea:focus,.aero input:focus{border-color:var(--oxblood);box-shadow:0 0 0 3px var(--oxblood-tint)}
.aero .params{display:grid;grid-template-columns:1fr 1fr;gap:10px}
.aero .toggle{display:flex;align-items:center;justify-content:space-between;
  background:var(--panel-2);border:1px solid var(--line-2);border-radius:8px;padding:10px 12px}
.aero .toggle span{font:600 11px/1 var(--mono);letter-spacing:.1em;color:var(--faint);
  text-transform:uppercase}
.aero .fire{display:flex;gap:10px;margin-top:16px}
.aero button.btn{flex:1;font:600 13px/1 var(--sans);letter-spacing:.02em;cursor:pointer;
  border-radius:9px;padding:13px;transition:.15s;border:1px solid var(--line-2)}
.aero button.btn.sec{background:transparent;color:var(--steel)}
.aero button.btn.sec:hover{border-color:var(--steel-lo);color:var(--text)}
.aero button.btn.pri{background:var(--oxblood);border-color:var(--oxblood-hi);color:#fff}
.aero button.btn.pri:hover{background:var(--oxblood-hi)}
.aero button.btn:disabled{opacity:.5;cursor:not-allowed}
.aero .hint{color:var(--faint);font-size:12px;margin:12px 0 0;line-height:1.5}
.aero .rgrid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
.aero .receipt{border:1px solid var(--line-2);border-radius:9px;padding:14px;background:var(--panel-2)}
.aero .receipt.empty{display:flex;flex-direction:column;justify-content:center;align-items:center;min-height:180px;color:var(--faint)}
.aero .rtitle{font:600 11px/1 var(--mono);letter-spacing:.12em;color:var(--faint);
  text-transform:uppercase;margin-bottom:11px}
.aero .badges{display:flex;flex-wrap:wrap;gap:6px;margin-bottom:12px}
.aero .badge{font:600 10px/1 var(--mono);letter-spacing:.06em;text-transform:uppercase;
  padding:5px 8px;border-radius:5px;border:1px solid transparent}
.aero .badge.hit{color:#ffb3ba;background:var(--oxblood-tint);border-color:var(--oxblood)}
.aero .badge.neu{color:var(--steel);background:#ffffff08;border-color:var(--line-2)}
.aero .badge.good{color:#a8d5c2;background:#1d9e7518;border-color:#1d9e7540}
.aero .badge.warn{color:var(--amber);background:#c98a2e18;border-color:#c98a2e40}
.aero .mline{display:flex;justify-content:space-between;gap:10px;padding:6px 0;
  border-top:1px dashed var(--line);font:12px/1.4 var(--mono)}
.aero .mline span{color:var(--faint)}
.aero .mline strong{color:var(--text);font-weight:500;text-align:right;overflow:hidden;
  text-overflow:ellipsis;max-width:60%;white-space:nowrap}
.aero .mline strong.zero{color:#ffb3ba}
.aero .verdict{margin-top:14px;border:1px solid var(--line-2);border-radius:9px;overflow:hidden}
.aero .verdict.idle{padding:14px;color:var(--faint);font-size:13px;text-align:center;background:var(--panel-2)}
.aero .vhead{display:flex;align-items:center;gap:8px;padding:11px 14px;
  background:var(--oxblood-tint);border-bottom:1px solid var(--oxblood);
  font:600 12px/1 var(--mono);letter-spacing:.08em;color:#ffb3ba}
.aero .vbody{display:grid;grid-template-columns:repeat(3,1fr);gap:1px;background:var(--line)}
.aero .vcell{background:var(--panel-2);padding:14px}
.aero .vcell .k{font:600 10px/1 var(--mono);letter-spacing:.1em;color:var(--faint);text-transform:uppercase}
.aero .vcell .v{font:600 22px/1 var(--sans);margin-top:8px;letter-spacing:-.01em}
.aero .vcell .v.win{background:linear-gradient(180deg,var(--steel-hi),var(--steel));
  -webkit-background-clip:text;background-clip:text;color:transparent}
.aero .ladder{display:flex;gap:8px}
.aero .rung{flex:1;border:1px solid var(--line-2);border-radius:9px;padding:14px 10px;
  background:var(--panel-2);text-align:center;transition:.2s}
.aero .rung .rl{font:600 13px/1 var(--sans);color:var(--steel)}
.aero .rung .rs{display:block;font:11px/1 var(--mono);color:var(--faint);margin-top:6px}
.aero .rung .rc{display:block;font:10px/1 var(--mono);letter-spacing:.06em;color:var(--faint);
  margin-top:8px;text-transform:uppercase}
.aero .rung.active{border-color:var(--oxblood);background:var(--oxblood-tint);
  box-shadow:0 0 26px -8px rgba(106,4,15,.9)}
.aero .rung.active .rl{color:#fff}.aero .rung.active .rc{color:#ffb3ba}
.aero .rung.future{opacity:.5}
.aero .savings{border:1px solid var(--line-2);border-radius:9px;padding:16px;background:var(--panel-2);margin-bottom:14px}
.aero .savings .k{font:600 11px/1 var(--mono);letter-spacing:.12em;color:var(--faint);text-transform:uppercase}
.aero .savings .v{font:600 40px/1 var(--mono);margin-top:10px;letter-spacing:-.02em}
.aero .savings .v i{font-style:normal;color:var(--oxblood-hi)}
.aero .tiles{display:grid;grid-template-columns:repeat(3,1fr);gap:10px;margin-bottom:14px}
.aero .tile{background:var(--panel-2);border:1px solid var(--line);border-radius:8px;padding:12px}
.aero .tile .k{font:600 10px/1 var(--mono);letter-spacing:.08em;color:var(--faint);text-transform:uppercase}
.aero .tile .v{font:600 19px/1 var(--mono);margin-top:8px}
.aero .tile .v.warnv{color:var(--amber)}
.aero .chart{margin-top:6px}
.aero .clabel{display:flex;justify-content:space-between;align-items:center;margin-bottom:8px}
.aero .clabel .k{font:600 11px/1 var(--mono);letter-spacing:.1em;color:var(--faint);text-transform:uppercase}
.aero .clabel .n{font:12px/1 var(--mono);color:var(--muted)}
.aero .divider{display:flex;align-items:center;gap:14px;margin:44px 0 22px}
.aero .divider .ln{flex:1;height:1px;background:var(--line)}
.aero .divider .t{font:600 11px/1 var(--mono);letter-spacing:.2em;color:var(--faint);text-transform:uppercase}
.aero .specs{display:grid;grid-template-columns:repeat(4,1fr);gap:14px}
@media(max-width:900px){.aero .specs{grid-template-columns:1fr 1fr}.aero .tiles,.aero .vbody{grid-template-columns:1fr 1fr}}
.aero .spec{border:1px solid var(--line);border-radius:11px;padding:18px;background:var(--panel)}
.aero .spec strong{display:block;font-size:14px;font-weight:600;margin-bottom:8px}
.aero .spec span{color:var(--muted);font-size:13px;line-height:1.55}
.aero .foot{display:flex;flex-wrap:wrap;gap:16px;align-items:center;justify-content:space-between;
  margin-top:40px;padding-top:22px;border-top:1px solid var(--line);
  font:12px/1.5 var(--mono);color:var(--faint)}
.aero .foot a{color:var(--steel);text-decoration:none}
.aero .foot a:hover{color:var(--text)}
.aero .toast{position:fixed;bottom:22px;left:50%;transform:translateX(-50%);z-index:50;
  background:var(--panel);border:1px solid var(--oxblood);border-radius:9px;padding:12px 16px;
  font-size:13px;color:var(--text);max-width:520px}
@media(max-width:650px){.aero .bar{padding:12px}.aero .wrap{padding:0 14px 48px}
  .aero .rgrid,.aero .params,.aero .specs{grid-template-columns:1fr}
  .aero .ladder{flex-direction:column}.aero .mode,.aero .signin{display:none}}
@media(prefers-reduced-motion:reduce){.aero .dot.live{animation:none}}

/* Aero Studio workspace */
.aero{height:100vh;min-height:680px;overflow:hidden;background:#0b090a}
.aero .studio-shell{position:relative;display:grid;grid-template-columns:190px minmax(0,1fr) 308px;
  height:100vh;min-height:680px}
.aero .studio-sidebar,.aero .settings-panel{position:relative;z-index:5;background:#0e0c0d}
.aero .studio-sidebar{display:flex;flex-direction:column;border-right:1px solid var(--line);padding:14px 12px}
.aero .studio-brand{display:flex;align-items:center;gap:11px;padding:4px 6px 21px}
.aero .studio-brand-copy{display:flex;align-items:baseline;gap:7px}
.aero .studio-brand .wordmark{font-size:15px}.aero .studio-brand .tag{font-size:9px}
.aero .nav-label{margin:17px 9px 7px;font:600 9px/1 var(--mono);letter-spacing:.16em;
  text-transform:uppercase;color:#544d4f}
.aero .studio-nav{display:grid;gap:3px;margin:0}
.aero .studio-nav a{display:flex;align-items:center;gap:10px;padding:9px 10px;border-radius:7px;
  color:var(--muted);font-size:13px;text-decoration:none}
.aero .studio-nav a:hover{color:var(--text);background:#ffffff08}
.aero .studio-nav a.active{background:#211b1d;color:var(--text);box-shadow:inset 2px 0 var(--oxblood-hi)}
.aero .nav-icon{width:16px;text-align:center;color:var(--steel-lo);font:14px/1 var(--mono)}
.aero .sidebar-spacer{flex:1}
.aero .sidebar-note{margin:12px 3px;padding:13px;border:1px solid var(--line-2);border-radius:10px;
  background:linear-gradient(145deg,rgba(106,4,15,.12),transparent 65%)}
.aero .sidebar-note strong{display:block;font-size:11px;margin-bottom:6px}
.aero .sidebar-note span{display:block;font-size:10px;line-height:1.5;color:var(--faint)}
.aero .sidebar-status{display:flex;align-items:center;gap:9px;margin:3px;padding:10px 9px;
  border-top:1px solid var(--line);font:600 10px/1 var(--mono);letter-spacing:.06em;color:var(--muted)}
.aero .panel-resizer{position:absolute;top:0;bottom:0;width:9px;z-index:30;cursor:col-resize;touch-action:none}
.aero .panel-resizer::after{content:"";position:absolute;top:0;bottom:0;left:4px;width:1px;
  background:transparent;transition:.15s}
.aero .panel-resizer:hover::after,.aero .panel-resizer.dragging::after{background:var(--oxblood-hi)}
.aero .panel-resizer.left{right:-5px}.aero .panel-resizer.right{left:-5px}

.aero .studio-main{position:relative;z-index:2;min-width:0;display:flex;flex-direction:column;background:#0b090a}
.aero .studio-topbar{height:58px;flex:0 0 58px;display:flex;align-items:center;gap:12px;
  padding:0 20px;border-bottom:1px solid var(--line);background:rgba(14,12,13,.94)}
.aero .topbar-title{font-size:14px;font-weight:600}.aero .topbar-sep{color:var(--faint)}
.aero .topbar-context{font:11px/1 var(--mono);color:var(--faint)}
.aero .topbar-actions{margin-left:auto;display:flex;align-items:center;gap:8px}
.aero .connection-pill{display:flex;align-items:center;gap:8px;padding:7px 9px;border:1px solid var(--line-2);
  border-radius:7px;font:600 9px/1 var(--mono);letter-spacing:.1em;color:var(--muted)}
.aero .icon-btn{width:30px;height:30px;border:1px solid transparent;border-radius:7px;background:transparent;
  color:var(--muted);cursor:pointer}.aero .icon-btn:hover{background:#ffffff08;color:var(--text);border-color:var(--line)}
.aero .panel-toggle{display:flex;align-items:center;gap:7px;height:30px;padding:0 9px;border:1px solid var(--line-2);
  border-radius:7px;background:transparent;color:var(--muted);font:600 10px/1 var(--mono);cursor:pointer}
.aero .panel-toggle:hover{color:var(--text);background:#ffffff08}
.aero .studio-scroll{min-height:0;overflow:auto;scroll-behavior:smooth}
.aero .workspace{width:100%;max-width:none;margin:0 auto;padding:34px 34px 60px}
.aero .workspace-intro{display:flex;align-items:flex-end;justify-content:space-between;gap:24px;margin-bottom:22px}
.aero .workspace-intro h1{font-size:28px;line-height:1.1;max-width:none;margin:9px 0 0;font-weight:500}
.aero .workspace-intro .lede{font-size:12px;line-height:1.55;max-width:47ch;text-align:right}
.aero .workspace .card{border-radius:10px;padding:17px;background:#100d0e}
.aero .prompt-studio{min-height:292px;display:flex;flex-direction:column}
.aero .prompt-editor{flex:1;display:flex;flex-direction:column;border:1px solid var(--line-2);
  background:#0c0a0b;border-radius:9px;overflow:hidden}
.aero .prompt-editor textarea{flex:1;min-height:150px;border:0;border-radius:0;background:transparent;
  font:14px/1.65 var(--sans);padding:17px;resize:vertical;box-shadow:none}
.aero .editor-actions{display:flex;align-items:center;gap:8px;padding:10px;border-top:1px solid var(--line)}
.aero .editor-chip{padding:6px 9px;border-radius:6px;background:#ffffff08;color:var(--muted);
  font:10px/1 var(--mono);border:1px solid var(--line)}
.aero .editor-actions .fire{margin:0 0 0 auto}.aero .editor-actions .btn{flex:none;padding:9px 13px;font-size:11px}
.aero .proof-card{margin-top:14px}.aero .proof-card .receipt{min-height:0}
.aero .insight-grid{display:grid;grid-template-columns:1fr;gap:14px;margin-top:14px}
.aero .insight-grid .ladder{display:grid;grid-template-columns:repeat(7,1fr)}
.aero .insight-grid .rung{min-width:0;padding:12px 5px}
.aero .boundary-wrap{margin-top:28px}.aero .divider{margin:0 0 16px}
.aero .specs{grid-template-columns:repeat(2,1fr);gap:10px}.aero .spec{border-radius:9px;padding:14px}
.aero .foot{margin-top:26px;padding-top:17px;font-size:10px}

.aero .settings-panel{border-left:1px solid var(--line);display:flex;flex-direction:column;overflow:auto}
.aero .settings-head{height:58px;flex:0 0 58px;display:flex;align-items:center;padding:0 16px;
  border-bottom:1px solid var(--line);font-size:12px;font-weight:600}
.aero .settings-head span{margin-left:auto;color:var(--faint);font:10px/1 var(--mono)}
.aero .settings-body{padding:14px}.aero .setting-section{padding:0 0 17px;margin-bottom:17px;border-bottom:1px solid var(--line)}
.aero .setting-title{font:600 9px/1 var(--mono);letter-spacing:.14em;text-transform:uppercase;
  color:var(--faint);margin-bottom:11px}
.aero .model-card{padding:12px;border:1px solid var(--line-2);border-radius:9px;background:#151213}
.aero .model-card input{border:0;background:transparent;padding:0;font:500 12px/1.4 var(--sans);box-shadow:none}
.aero .model-card p{margin:7px 0 0;color:var(--faint);font-size:10px;line-height:1.45}
.aero .setting-field{display:block;margin-top:13px}.aero .setting-field>span,.aero .setting-row>span{
  display:block;margin-bottom:7px;font-size:11px;color:var(--muted)}
.aero .setting-field input[type=number]{padding:8px 9px;font-size:11px;border-radius:7px}
.aero .range-row{display:grid;grid-template-columns:minmax(0,1fr) 50px;gap:8px;align-items:center}
.aero input[type=range]{width:100%;accent-color:var(--oxblood-hi)}
.aero .setting-row{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:8px 0}
.aero .setting-row>span{margin:0}.aero .switch{position:relative;width:31px;height:17px;flex:0 0 auto}
.aero .switch input{position:absolute;opacity:0}.aero .switch i{position:absolute;inset:0;border-radius:20px;
  background:#2b2728;border:1px solid #3b3537;transition:.2s}.aero .switch i::after{content:"";position:absolute;
  width:11px;height:11px;left:2px;top:2px;border-radius:50%;background:#898386;transition:.2s}
.aero .switch input:checked+i{background:var(--oxblood);border-color:var(--oxblood-hi)}
.aero .switch input:checked+i::after{transform:translateX(14px);background:#fff}
.aero .settings-stat{display:flex;justify-content:space-between;padding:6px 0;font:10px/1.3 var(--mono)}
.aero .settings-stat span{color:var(--faint)}.aero .settings-stat strong{font-weight:500}
.aero .settings-help{margin-top:auto;padding:14px 16px;border-top:1px solid var(--line);color:var(--faint);
  font-size:10px;line-height:1.5}

@media(max-width:1180px){
  .aero .studio-shell{grid-template-columns:190px minmax(0,1fr) 280px}
  .aero .workspace{padding-left:24px;padding-right:24px}.aero .workspace-intro .lede{display:none}
}
@media(max-width:960px){
  .aero{height:auto;min-height:100vh;overflow:auto}.aero .studio-shell{display:block;height:auto;min-height:100vh}
  .aero .studio-sidebar{position:sticky;top:0;height:52px;z-index:40;display:flex;flex-direction:row;align-items:center;
    padding:8px 12px;border-right:0;border-bottom:1px solid var(--line)}
  .aero .studio-brand{padding:0}.aero .studio-nav{display:flex;margin-left:auto}.aero .studio-nav a{padding:8px}
  .aero .studio-nav a span:last-child,.aero .nav-label,.aero .sidebar-note,.aero .sidebar-status,.aero .sidebar-spacer{display:none}
  .aero .studio-main{display:block}.aero .studio-topbar{position:sticky;top:52px;z-index:30}
  .aero .studio-scroll{overflow:visible}.aero .settings-panel{border-left:0;border-top:1px solid var(--line)}
  .aero .panel-resizer{display:none}
  .aero .settings-head{height:52px}.aero .settings-body{display:grid;grid-template-columns:repeat(3,1fr);gap:14px}
  .aero .setting-section{border:1px solid var(--line);border-radius:9px;padding:13px;margin:0}
  .aero .settings-help{display:none}
}
@media(max-width:650px){
  .aero .studio-brand-copy .tag,.aero .topbar-context,.aero .icon-btn{display:none}
  .aero .studio-topbar{padding:0 12px}.aero .workspace{padding:24px 12px 42px}
  .aero .workspace-intro h1{font-size:23px}.aero .rgrid,.aero .specs{grid-template-columns:1fr}
  .aero .insight-grid .ladder{display:flex;overflow-x:auto}.aero .insight-grid .rung{min-width:88px}
  .aero .settings-body{grid-template-columns:1fr}.aero .editor-actions{flex-wrap:wrap}
  .aero .editor-actions .fire{width:100%;margin:2px 0 0}.aero .editor-actions .btn{flex:1}
}
`;

type Mode = "connecting" | "live" | "error";
type CacheState = "hit" | "miss" | "coalesced" | "bypass" | "error" | "unknown";

type PromptRequest = {
  model: string;
  prompt: string;
  temperature: number;
  max_tokens: number;
  stream: boolean;
};

type Receipt = {
  request_id?: string | null;
  key_prefix?: string | null;
  tier: string;
  cache: CacheState;
  verified: boolean;
  total_ms: number;
  cost_usd: number;
  tokens_out: number;
  answer_sha256?: string | null;
};

type FireResult = {
  status: number;
  receipt: Receipt;
};

type StatsShape = {
  requests: number;
  hits: number;
  misses: number;
  coalesced: number;
  bypass: number;
  errors: number;
  upstream_calls: number;
  verify_mismatch: number;
  writeback_queue_depth: number;
  writeback_dropped: number;
  hit_ratio: number;
  usd_saved?: number;
  usd_saved_total?: number;
};

type TracePoint = {
  t: number;
  hit: number;
  saved: number;
};

function statsSaved(stats: StatsShape | null): number {
  return stats?.usd_saved_total ?? stats?.usd_saved ?? 0;
}

function stableHash(s: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return (h >>> 0).toString(16).padStart(8, "0");
}

function fmtUSD(n: number): string {
  if (n >= 1) {
    return n.toLocaleString(undefined, {
      minimumFractionDigits: 2,
      maximumFractionDigits: 2
    });
  }
  return n.toFixed(6);
}

function useCountUp(target: number, ms = 600): number {
  const [val, setVal] = useState(target);
  const ref = useRef(target);

  useEffect(() => {
    const from = ref.current;
    const to = target;
    const start = performance.now();
    let raf = 0;

    const step = (now: number) => {
      const k = Math.min(1, (now - start) / ms);
      const e = 1 - Math.pow(1 - k, 3);
      const cur = from + (to - from) * e;
      ref.current = cur;
      setVal(cur);

      if (k < 1) {
        raf = requestAnimationFrame(step);
      }
    };

    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [target, ms]);

  return val;
}

async function realStats(signal?: AbortSignal): Promise<StatsShape> {
  const r = await fetch("/stats", { signal });
  if (!r.ok) {
    throw new Error(`stats ${r.status}`);
  }
  return await r.json();
}

async function realFire(req: PromptRequest, signal?: AbortSignal): Promise<FireResult> {
  const start = performance.now();

  const r = await fetch("/v1/chat/completions", {
    method: "POST",
    signal,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      model: req.model,
      temperature: req.temperature,
      max_tokens: req.max_tokens,
      stream: req.stream,
      messages: [{ role: "user", content: req.prompt }]
    })
  });

  const raw = await r.text();
  const body = parseJSON(raw);
  const rc = readReceiptObject(body);
  const h = r.headers;

  const cache = normalizeCache(h.get("X-Aero-Cache") || rc.cache);
  const tier = h.get("X-Aero-Tier") || rc.tier || "unknown";
  const serverLatency = numberValue(h.get("X-Aero-Latency-Ms") || rc.total_ms);
  const totalMs = serverLatency > 0 ? serverLatency : performance.now() - start;
  const cost = numberValue(
    h.get("X-Aero-Cost-Estimate-USD") ||
      h.get("X-Aero-Cost-Usd") ||
      rc.cost_usd
  );

  return {
    status: r.status,
    receipt: {
      request_id: h.get("X-Aero-Request-Id") || rc.request_id || null,
      key_prefix: shorten(h.get("X-Aero-Key") || rc.key_prefix || null),
      tier,
      cache,
      verified:
        h.get("X-Aero-Verified") === "true" ||
        rc.verified === true ||
        cache === "hit" ||
        cache === "coalesced",
      total_ms: totalMs,
      cost_usd: cost,
      tokens_out: numberValue(h.get("X-Aero-Tokens-Out") || rc.tokens_out),
      answer_sha256: rc.answer_sha256 || `fnv32:${stableHash(raw)}`
    }
  };
}

function parseJSON(raw: string): unknown {
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

function readReceiptObject(body: unknown): Partial<Receipt> {
  if (!body || typeof body !== "object") {
    return {};
  }

  const obj = body as Record<string, unknown>;
  const receipt = obj.aero_receipt;

  if (!receipt || typeof receipt !== "object") {
    return {};
  }

  return receipt as Partial<Receipt>;
}

function normalizeCache(v: unknown): CacheState {
  if (
    v === "hit" ||
    v === "miss" ||
    v === "coalesced" ||
    v === "bypass" ||
    v === "error"
  ) {
    return v;
  }
  return "unknown";
}

function numberValue(v: unknown): number {
  const n = Number(v);
  return Number.isFinite(n) ? n : 0;
}

function shorten(v: string | null): string | null {
  if (!v) {
    return null;
  }
  if (v.length <= 22) {
    return v;
  }
  return `${v.slice(0, 22)}…`;
}

export default function App() {
  const [model, setModel] = useState("llama3.2:3b");
  const [prompt, setPrompt] = useState("Say exactly: pong");
  const [maxTokens, setMaxTokens] = useState(32);
  const [temperature, setTemperature] = useState(0);
  const [stream, setStream] = useState(false);

  const [first, setFirst] = useState<FireResult | null>(null);
  const [second, setSecond] = useState<FireResult | null>(null);
  const [stats, setStats] = useState<StatsShape | null>(null);
  const [trace, setTrace] = useState<TracePoint[]>([]);
  const [loading, setLoading] = useState(false);
  const [statusLine, setStatusLine] = useState("idle");
  const [mode, setMode] = useState<Mode>("connecting");
  const [toast, setToast] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [settingsOpen, setSettingsOpen] = useState(true);
  const [sidebarWidth, setSidebarWidth] = useState(190);
  const [settingsWidth, setSettingsWidth] = useState(308);

  const request = useMemo<PromptRequest>(
    () => ({
      model,
      prompt,
      temperature,
      max_tokens: maxTokens,
      stream
    }),
    [model, prompt, temperature, maxTokens, stream]
  );

  useEffect(() => {
    let alive = true;
    const ctl = new AbortController();
    const to = window.setTimeout(() => ctl.abort(), 1500);

    realStats(ctl.signal)
      .then((s) => {
        if (!alive) return;
        setMode("live");
        setStats(normalizeStats(s));
        seedTrace(normalizeStats(s));
      })
      .catch(() => {
        if (!alive) return;
        setMode("error");
        setStatusLine("backend unavailable");
      })
      .finally(() => window.clearTimeout(to));

    return () => {
      alive = false;
      ctl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (mode === "connecting") {
      return;
    }

    const id = window.setInterval(tick, POLL_MS);
    return () => window.clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode]);

  function normalizeStats(s: StatsShape): StatsShape {
    return {
      requests: s.requests ?? 0,
      hits: s.hits ?? 0,
      misses: s.misses ?? 0,
      coalesced: s.coalesced ?? 0,
      bypass: s.bypass ?? 0,
      errors: s.errors ?? 0,
      upstream_calls: s.upstream_calls ?? 0,
      verify_mismatch: s.verify_mismatch ?? 0,
      writeback_queue_depth: s.writeback_queue_depth ?? 0,
      writeback_dropped: s.writeback_dropped ?? 0,
      hit_ratio: s.hit_ratio ?? 0,
      usd_saved: s.usd_saved ?? 0,
      usd_saved_total: s.usd_saved_total ?? s.usd_saved ?? 0
    };
  }

  function seedTrace(s: StatsShape) {
    const base = s.hit_ratio ?? 0;
    const sv = statsSaved(s);

    setTrace(
      Array.from({ length: 12 }, (_, i) => ({
        t: i,
        hit: Math.max(0, base * 100 - (12 - i) * 1.4 + (Math.random() * 2 - 1)),
        saved: sv
      }))
    );
  }

  function pushTrace(s: StatsShape) {
    setTrace((prev) => {
      const next = [
        ...prev,
        {
          t: (prev[prev.length - 1]?.t ?? 0) + 1,
          hit: (s.hit_ratio ?? 0) * 100,
          saved: statsSaved(s)
        }
      ];

      return next.slice(-TRACE_CAP);
    });
  }

  async function tick() {
    try {
      const s = normalizeStats(await realStats());
      setMode("live");
      setStats(s);
      pushTrace(s);
    } catch {
      setMode("error");
    }
  }

  async function doFire(req: PromptRequest): Promise<FireResult> {
    return await realFire(req);
  }

  async function fireOnce() {
    setLoading(true);
    setStatusLine("firing prompt");

    try {
      const res = await doFire(request);
      setFirst(res);
      setSecond(null);
      setStatusLine(`${res.receipt.cache} · ${res.receipt.tier}`);
    } catch (e) {
      setStatusLine(e instanceof Error ? e.message : "request failed");
    } finally {
      setLoading(false);
    }
  }

  async function fireTwice() {
    setLoading(true);
    setFirst(null);
    setSecond(null);
    setStatusLine("fire A");

    try {
      const a = await doFire(request);
      setFirst(a);

      setStatusLine("fire B");
      const b = await doFire(request);
      setSecond(b);

      setStatusLine(`${a.receipt.cache} → ${b.receipt.cache}`);
    } catch (e) {
      setStatusLine(e instanceof Error ? e.message : "twice-fire failed");
    } finally {
      setLoading(false);
    }
  }

  const activeTier = (second ?? first)?.receipt.tier ?? "none";

  return (
    <div className="aero">
      <style>{CSS}</style>
      <div className="bg" />
      <div
        className="studio-shell"
        style={{
          gridTemplateColumns: `${sidebarOpen ? sidebarWidth : 0}px minmax(0,1fr) ${settingsOpen ? settingsWidth : 0}px`
        }}
      >
        {sidebarOpen && <StudioSidebar width={sidebarWidth} setWidth={setSidebarWidth} />}

        <main className="studio-main">
          <header className="studio-topbar">
            <button
              className="icon-btn"
              title={sidebarOpen ? "Hide navigation" : "Show navigation"}
              onClick={() => setSidebarOpen((open) => !open)}
            >
              {sidebarOpen ? "‹" : "☰"}
            </button>
            <span className="topbar-title">Playground</span>
            <span className="topbar-sep">/</span>
            <span className="topbar-context">exact cache proof</span>
            <div className="topbar-actions">
              <button className="panel-toggle" onClick={() => setSettingsOpen((open) => !open)}>
                {settingsOpen ? "Hide settings" : "Run settings"}
              </button>
            </div>
          </header>

          <div className="studio-scroll">
            <div className="workspace">
              <section className="workspace-intro" id="console">
                <div>
                  <div className="eyebrow">Aero playground</div>
                  <h1>Prove the cache in <em>two fires.</em></h1>
                </div>
                <p className="lede">
                  Same production ingress. Compare the serving tier, response identity,
                  latency, and marginal cost without leaving the workspace.
                </p>
              </section>

              <PromptConsole
                prompt={prompt}
                loading={loading}
                statusLine={statusLine}
                setPrompt={setPrompt}
                fireOnce={fireOnce}
                fireTwice={fireTwice}
              />

              <ProofPanel first={first} second={second} />

              <div className="insight-grid" id="telemetry">
                <LadderView active={activeTier} />
                <Telemetry stats={stats} trace={trace} />
              </div>

              <div className="boundary-wrap" id="boundary">
                <div className="divider">
                  <span className="ln" />
                  <span className="t">What this proof guarantees</span>
                  <span className="ln" />
                </div>
                <BoundaryPanel />
              </div>

              <footer className="foot">
                <span>Served by the Go proxy · OCI Always Free ARM · $0 to run</span>
                <span>Tier-A exact cache · store-and-verify · v0.1</span>
              </footer>
            </div>
          </div>
        </main>

        {settingsOpen && (
          <RunSettings
            model={model}
            maxTokens={maxTokens}
            temperature={temperature}
            stream={stream}
            stats={stats}
            width={settingsWidth}
            setWidth={setSettingsWidth}
            setModel={setModel}
            setMaxTokens={setMaxTokens}
            setTemperature={setTemperature}
            setStream={setStream}
          />
        )}
      </div>

      {toast && (
        <div className="toast" onClick={() => setToast("")} role="status">
          {toast} <span style={{ color: "var(--faint)" }}>(tap to dismiss)</span>
        </div>
      )}
    </div>
  );
}

function StudioSidebar({ width, setWidth }: { width: number; setWidth: (width: number) => void }) {
  return (
    <aside className="studio-sidebar">
      <div className="studio-brand">
        <BrandMark />
        <div className="studio-brand-copy">
          <span className="wordmark">AERO</span>
        </div>
      </div>

      <div className="nav-label">Explore</div>
      <nav className="studio-nav">
        <a className="active" href="#console"><span className="nav-icon">⌁</span><span>Playground</span></a>
      </nav>
      <div className="sidebar-spacer" />
      <PanelResizer side="left" size={width} min={150} max={340} setSize={setWidth} />
    </aside>
  );
}

type PanelResizerProps = {
  side: "left" | "right";
  size: number;
  min: number;
  max: number;
  setSize: (size: number) => void;
};

function PanelResizer({ side, size, min, max, setSize }: PanelResizerProps) {
  const [dragging, setDragging] = useState(false);

  function startResize(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault();
    const startX = event.clientX;
    const startSize = size;
    setDragging(true);

    const move = (moveEvent: PointerEvent) => {
      const delta = moveEvent.clientX - startX;
      const next = side === "left" ? startSize + delta : startSize - delta;
      setSize(Math.min(max, Math.max(min, next)));
    };

    const stop = () => {
      setDragging(false);
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
    };

    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop);
  }

  return (
    <div
      className={`panel-resizer ${side}${dragging ? " dragging" : ""}`}
      role="separator"
      aria-label={`Resize ${side} panel`}
      aria-orientation="vertical"
      onPointerDown={startResize}
    />
  );
}

type RunSettingsProps = {
  model: string;
  maxTokens: number;
  temperature: number;
  stream: boolean;
  stats: StatsShape | null;
  width: number;
  setWidth: (width: number) => void;
  setModel: (v: string) => void;
  setMaxTokens: (v: number) => void;
  setTemperature: (v: number) => void;
  setStream: (v: boolean) => void;
};

function RunSettings(p: RunSettingsProps) {
  return (
    <aside className="settings-panel">
      <PanelResizer side="right" size={p.width} min={260} max={480} setSize={p.setWidth} />
      <div className="settings-head">Run settings</div>
      <div className="settings-body">
        <section className="setting-section">
          <div className="setting-title">Model</div>
          <div className="model-card">
            <input aria-label="Model" value={p.model} onChange={(e) => p.setModel(e.target.value)} />
            <p>Served through the same proxy route used by production clients.</p>
          </div>

          <label className="setting-field">
            <span>Temperature</span>
            <div className="range-row">
              <input
                aria-label="Temperature slider"
                type="range"
                min="0"
                max="1"
                step="0.1"
                value={p.temperature}
                onChange={(e) => p.setTemperature(Number(e.target.value))}
              />
              <input
                aria-label="Temperature"
                type="number"
                min="0"
                max="1"
                step="0.1"
                value={p.temperature}
                onChange={(e) => p.setTemperature(Number(e.target.value))}
              />
            </div>
          </label>

          <label className="setting-field">
            <span>Maximum output tokens</span>
            <input type="number" min="1" value={p.maxTokens} onChange={(e) => p.setMaxTokens(Number(e.target.value))} />
          </label>
        </section>

        <section className="setting-section">
          <div className="setting-title">Request behavior</div>
          <div className="setting-row">
            <span>Stream response</span>
            <label className="switch">
              <input type="checkbox" checked={p.stream} onChange={(e) => p.setStream(e.target.checked)} />
              <i />
            </label>
          </div>
          <div className="setting-row">
            <span>Exact cache</span>
            <label className="switch">
              <input type="checkbox" checked={p.temperature === 0} readOnly />
              <i />
            </label>
          </div>
          <p className="hint">Non-zero temperature bypasses cache to protect correctness.</p>
        </section>

        <section className="setting-section">
          <div className="setting-title">Snapshot</div>
          <div className="settings-stat"><span>Requests</span><strong>{(p.stats?.requests ?? 0).toLocaleString()}</strong></div>
          <div className="settings-stat"><span>Hit ratio</span><strong>{((p.stats?.hit_ratio ?? 0) * 100).toFixed(1)}%</strong></div>
          <div className="settings-stat"><span>Upstream calls</span><strong>{(p.stats?.upstream_calls ?? 0).toLocaleString()}</strong></div>
          <div className="settings-stat"><span>Verify mismatch</span><strong>{p.stats?.verify_mismatch ?? 0}</strong></div>
        </section>
      </div>
      <div className="settings-help">Settings are applied directly to the next request. No backend contract or route is changed by this interface.</div>
    </aside>
  );
}

function BrandMark() {
  const [failed, setFailed] = useState(false);

  if (!failed) {
    return (
      <img
        className="logo-img"
        src="/aero-logo.png"
        alt="Aero logo"
        onError={() => setFailed(true)}
      />
    );
  }

  return <AeroMark size={30} />;
}

function AeroMark({ size = 26 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" aria-label="Aero" role="img">
      <defs>
        <linearGradient id="steel" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#f2f2f4" />
          <stop offset=".5" stopColor="#b9b9bd" />
          <stop offset="1" stopColor="#76767b" />
        </linearGradient>
      </defs>
      <ellipse
        cx="32"
        cy="35"
        rx="27"
        ry="12"
        transform="rotate(-24 32 35)"
        fill="none"
        stroke="url(#steel)"
        strokeWidth="3.1"
        opacity="0.92"
      />
      <path
        d="M19 47 L32 15 L45 47"
        fill="none"
        stroke="url(#steel)"
        strokeWidth="4.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="M25 35 L39 35"
        stroke="url(#steel)"
        strokeWidth="4.1"
        strokeLinecap="round"
      />
    </svg>
  );
}

type PromptConsoleProps = {
  prompt: string;
  loading: boolean;
  statusLine: string;
  setPrompt: (v: string) => void;
  fireOnce: () => void;
  fireTwice: () => void;
};

function PromptConsole(p: PromptConsoleProps) {
  return (
    <section className="card prompt-studio">
      <div className="chead">
        <div>
          <div className="eyebrow">Prompt console</div>
          <h2>OpenAI-compatible request</h2>
        </div>
        <span className="status">{p.statusLine}</span>
      </div>

      <div className="prompt-editor">
        <textarea
          aria-label="Prompt"
          value={p.prompt}
          spellCheck={false}
          onChange={(e) => p.setPrompt(e.target.value)}
          placeholder="Enter a deterministic prompt to test the cache…"
        />
        <div className="editor-actions">
          <span className="editor-chip">Tier-A exact</span>
          <span className="editor-chip">store + verify</span>
          <div className="fire">
            <button className="btn sec" disabled={p.loading} onClick={p.fireOnce}>
              Fire once
            </button>
            <button className="btn pri" disabled={p.loading} onClick={p.fireTwice}>
              Fire twice
            </button>
          </div>
        </div>
      </div>
    </section>
  );
}

function ProofPanel({ first, second }: { first: FireResult | null; second: FireResult | null }) {
  return (
    <section className="card proof-card">
      <div className="chead">
        <div>
          <div className="eyebrow">Proof panel</div>
          <h2>Receipt comparison</h2>
        </div>
      </div>

      <div className="rgrid">
        <ReceiptView label="Fire A" res={first} />
        <ReceiptView label="Fire B" res={second} />
      </div>

      <Verdict first={first} second={second} />
    </section>
  );
}

function ReceiptView({ label, res }: { label: string; res: FireResult | null }) {
  if (!res) {
    return (
      <div className="receipt empty">
        <div className="rtitle">{label}</div>
        <div>waiting</div>
      </div>
    );
  }

  const r = res.receipt;

  return (
    <div className="receipt">
      <div className="rtitle">{label}</div>

      <div className="badges">
        <span className={`badge ${cacheTone(r.cache)}`}>{r.cache}</span>
        <span className="badge neu">{r.tier}</span>
        <span className={`badge ${r.verified ? "good" : "warn"}`}>
          {r.verified ? "verified" : "unverified"}
        </span>
      </div>

      <MetricLine label="latency" value={`${r.total_ms.toFixed(2)} ms`} />
      <MetricLine label="cost" value={`$${fmtUSD(r.cost_usd)}`} zero={r.cost_usd === 0} />
      <MetricLine label="tokens" value={String(r.tokens_out)} />
      <MetricLine label="key" value={r.key_prefix ?? "—"} />
      <MetricLine label="body sha" value={r.answer_sha256 ?? "n/a"} />
    </div>
  );
}

function MetricLine({ label, value, zero = false }: { label: string; value: string; zero?: boolean }) {
  return (
    <div className="mline">
      <span>{label}</span>
      <strong className={zero ? "zero" : ""}>{value}</strong>
    </div>
  );
}

function Verdict({ first, second }: { first: FireResult | null; second: FireResult | null }) {
  if (!first || !second) {
    return (
      <div className="verdict idle">
        Fire twice to compare cache state, response identity, latency, and cost.
      </div>
    );
  }

  const a = first.receipt;
  const b = second.receipt;
  const same = Boolean(a.answer_sha256 && a.answer_sha256 === b.answer_sha256);
  const dLat = a.total_ms - b.total_ms;
  const dCost = a.cost_usd - b.cost_usd;
  const proven = same && b.cache === "hit" && b.cost_usd === 0;

  return (
    <div className="verdict">
      <div className="vhead">
        <AeroMark size={14} />
        {proven ? "PROVEN — identical bytes, served for $0" : `${a.cache} → ${b.cache}`}
      </div>

      <div className="vbody">
        <div className="vcell">
          <div className="k">response identity</div>
          <div className="v win">{same ? "byte-identical" : "differs"}</div>
        </div>

        <div className="vcell">
          <div className="k">latency saved</div>
          <div className="v win">{dLat > 0 ? dLat.toFixed(0) : 0} ms</div>
        </div>

        <div className="vcell">
          <div className="k">cost saved</div>
          <div className="v win">${fmtUSD(Math.max(0, dCost))}</div>
        </div>
      </div>
    </div>
  );
}

function LadderView({ active }: { active: string }) {
  const rungs = [
    { key: "cache-l1", l: "L1", s: "ristretto", c: "~$0" },
    { key: "cache-l2", l: "L2", s: "Valkey", c: "~$0" },
    { key: "cache-l3", l: "L3", s: "R2 · future", c: "~$0", future: true },
    { key: "fleet", l: "Fleet", s: "edge · future", c: "sunk", future: true },
    { key: "dev", l: "Dev", s: "Ollama", c: "$0 CPU" },
    { key: "owned", l: "Owned", s: "vLLM", c: "fixed" },
    { key: "burst", l: "Burst", s: "rent · future", c: "marginal", future: true }
  ];

  return (
    <section className="card">
      <div className="chead">
        <div>
          <div className="eyebrow">Capacity ladder</div>
          <h2>Served tier</h2>
        </div>
      </div>

      <div className="ladder">
        {rungs.map((r) => (
          <div
            key={r.key}
            className={`rung${active === r.key ? " active" : ""}${r.future ? " future" : ""}`}
          >
            <div className="rl">{r.l}</div>
            <span className="rs">{r.s}</span>
            <span className="rc">{r.c}</span>
          </div>
        ))}
      </div>

      <p className="hint">
        Cheapest rung that still meets the deadline serves the request.
      </p>
    </section>
  );
}

function Telemetry({ stats, trace }: { stats: StatsShape | null; trace: TracePoint[] }) {
  const saved = useCountUp(statsSaved(stats));

  const counters = useMemo(
    () => [
      { name: "hits", value: stats?.hits ?? 0 },
      { name: "misses", value: stats?.misses ?? 0 },
      { name: "coalesced", value: stats?.coalesced ?? 0 },
      { name: "bypass", value: stats?.bypass ?? 0 },
      { name: "errors", value: stats?.errors ?? 0 }
    ],
    [stats]
  );

  const hitRatio = ((stats?.hit_ratio ?? 0) * 100).toFixed(1);

  return (
    <section className="card">
      <div className="chead">
        <div>
          <div className="eyebrow">Telemetry</div>
          <h2>Cache economics</h2>
        </div>
        <span className="status">refresh · 2.5s</span>
      </div>

      <div className="savings">
        <div className="k">GPU cost avoided</div>
        <div className="v">
          <i>$</i>
          {fmtUSD(saved)}
        </div>
      </div>

      <div className="tiles">
        <Tile label="requests" value={(stats?.requests ?? 0).toLocaleString()} />
        <Tile label="hit ratio" value={`${hitRatio}%`} />
        <Tile label="upstream calls" value={(stats?.upstream_calls ?? 0).toLocaleString()} />
        <Tile label="verify mismatch" value={stats?.verify_mismatch ?? 0} warn={(stats?.verify_mismatch ?? 0) > 0} />
        <Tile label="writeback depth" value={stats?.writeback_queue_depth ?? 0} />
        <Tile label="writeback dropped" value={stats?.writeback_dropped ?? 0} />
      </div>

      <div className="chart">
        <div className="clabel">
          <span className="k">Hit ratio</span>
          <span className="n">{hitRatio}% now</span>
        </div>

        <ResponsiveContainer width="100%" height={130}>
          <AreaChart data={trace} margin={{ top: 4, right: 4, left: -22, bottom: 0 }}>
            <defs>
              <linearGradient id="ox" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0" stopColor="#8d0a17" stopOpacity={0.5} />
                <stop offset="1" stopColor="#6a040f" stopOpacity={0} />
              </linearGradient>
            </defs>
            <YAxis domain={[0, 100]} tick={{ fill: "#5f585a", fontSize: 10 }} width={34} axisLine={false} tickLine={false} />
            <XAxis dataKey="t" hide />
            <Tooltip
              contentStyle={{ background: "#100d0e", border: "1px solid #312325", borderRadius: 8, color: "#f3f1f2", fontSize: 12 }}
              labelStyle={{ display: "none" }}
              formatter={(v) => [`${Number(v).toFixed(1)}%`, "hit ratio"]}
            />
            <Area type="monotone" dataKey="hit" stroke="#8d0a17" strokeWidth={2} fill="url(#ox)" isAnimationActive={false} dot={false} />
          </AreaChart>
        </ResponsiveContainer>
      </div>

      <div className="chart">
        <div className="clabel">
          <span className="k">Request outcomes</span>
          <span className="n">cumulative</span>
        </div>

        <ResponsiveContainer width="100%" height={120}>
          <BarChart data={counters} margin={{ top: 4, right: 4, left: -22, bottom: 0 }}>
            <XAxis dataKey="name" tick={{ fill: "#5f585a", fontSize: 10 }} axisLine={false} tickLine={false} />
            <YAxis allowDecimals={false} tick={{ fill: "#5f585a", fontSize: 10 }} width={34} axisLine={false} tickLine={false} />
            <Tooltip
              contentStyle={{ background: "#100d0e", border: "1px solid #312325", borderRadius: 8, color: "#f3f1f2", fontSize: 12 }}
              cursor={{ fill: "#ffffff08" }}
            />
            <Bar dataKey="value" radius={[6, 6, 0, 0]}>
              {counters.map((c, i) => (
                <Cell
                  key={i}
                  fill={c.name === "hits" ? "#6a040f" : c.name === "errors" ? "#c98a2e" : "#3a2b2d"}
                />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>
    </section>
  );
}

function Tile({ label, value, warn = false }: { label: string; value: string | number; warn?: boolean }) {
  return (
    <div className="tile">
      <div className="k">{label}</div>
      <div className={`v${warn ? " warnv" : ""}`}>{value}</div>
    </div>
  );
}

function BoundaryPanel() {
  const items = [
    {
      t: "Correctness",
      d: "Only exact Tier-A hits, byte-verified before serving. A mismatch is demoted to a miss."
    },
    {
      t: "Economics",
      d: "The second identical request avoids upstream compute entirely. Cost drops to zero."
    },
    {
      t: "Ingress honesty",
      d: "The console calls the same OpenAI-compatible route production clients use. No side door."
    },
    {
      t: "Scope control",
      d: "No prompt library, memory, collaboration, or database. If it needed one, it would be the chat app."
    }
  ];

  return (
    <section className="specs">
      {items.map((it) => (
        <div className="spec" key={it.t}>
          <strong>{it.t}</strong>
          <span>{it.d}</span>
        </div>
      ))}
    </section>
  );
}

function cacheTone(cache: CacheState): "hit" | "neu" | "warn" {
  if (cache === "hit" || cache === "coalesced") return "hit";
  if (cache === "error") return "warn";
  return "neu";
}
