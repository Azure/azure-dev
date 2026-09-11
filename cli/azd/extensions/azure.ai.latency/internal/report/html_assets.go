// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//nolint:lll // CSS and JavaScript must remain byte-identical to the authoritative renderer.
package report

const htmlCSS = `
:root {
  --ink: #172033;
  --muted: #59677a;
  --line: #dbe2ea;
  --line-strong: #c8d2de;
  --page: #f4f6f8;
  --card: #ffffff;
  --surface: #f8fafc;
  --surface-strong: #eef2f6;
  --blue: #2563eb;
  --blue-soft: #edf4ff;
  --green: #047857;
  --green-soft: #edf8f3;
  --amber: #8a5800;
  --amber-soft: #fff8e7;
  --red: #b42318;
  --red-soft: #fff4f2;
  --shadow: 0 4px 16px rgba(15, 23, 42, .06);
}
*{box-sizing:border-box}
html{overflow-x:hidden;-webkit-text-size-adjust:100%}
body{min-width:320px;margin:0;color:var(--ink);background:var(--page);font:400 15px/1.5 "Segoe UI",Arial,sans-serif;-webkit-font-smoothing:antialiased;-moz-osx-font-smoothing:grayscale;text-rendering:optimizeLegibility}
button,input{font:inherit}
h1,h2,h3,h4,strong{font-weight:700}
.shell{width:min(1120px,calc(100% - 40px));margin:auto}
.topbar{padding:21px 0 25px;color:#fff;background:linear-gradient(120deg,#102b50,#1750a0 62%,#2563c9)}
.topbar strong{font-size:13px}
.topbar h1{margin:11px 0 2px;font-size:clamp(28px,3.6vw,34px);line-height:1.2;letter-spacing:-.015em}
.topbar p{margin:0;color:#e2ecfb;font-size:14px}
main{padding:20px 0 42px}
.card{min-width:0;border:1px solid var(--line);border-radius:13px;background:var(--card);box-shadow:var(--shadow)}
.scenario-picker{margin-bottom:16px;padding:14px 16px}
.scenario-picker>small{display:block;color:var(--muted);font-size:12px}
.scenario-picker>div{display:flex;flex-wrap:wrap;gap:7px;margin-top:10px}
.scenario-button{padding:7px 11px;border:1px solid var(--line-strong);border-radius:999px;background:var(--card);color:#34445f;cursor:pointer;font-size:12px;font-weight:600}
.scenario-button:hover{border-color:#8ea6c5;background:var(--surface)}
.scenario-button.active{color:#fff;border-color:var(--blue);background:var(--blue)}
.scenario-panel{display:none}
.scenario-panel.active{display:block}

.assessment-card{--state-color:var(--green);--state-soft:var(--green-soft);position:relative;display:grid;grid-template-columns:minmax(0,1.2fr) minmax(340px,.8fr);overflow:hidden;border-color:var(--line-strong)}
.assessment-card.state-danger{--state-color:var(--red);--state-soft:var(--red-soft)}
.assessment-card.state-warning{--state-color:var(--amber);--state-soft:var(--amber-soft)}
.assessment-card.state-info{--state-color:var(--blue);--state-soft:var(--blue-soft)}
.assessment-card:before{position:absolute;inset:0 auto 0 0;width:5px;background:var(--state-color);content:""}
.assessment-copy,.assessment-chart{min-width:0;padding:25px 27px}
.assessment-chart{display:flex;flex-direction:column;justify-content:center;border-left:1px solid var(--line);background:var(--surface)}
.status-line{display:flex;align-items:center;gap:10px}
.status-icon{display:grid;flex:0 0 auto;place-items:center;width:32px;height:32px;border-radius:50%;color:#fff;background:var(--state-color);font-size:16px;font-weight:700}
.status-badge,.pill{display:inline-block;padding:4px 9px;border-radius:999px;color:var(--green);background:var(--green-soft);font-size:11px;font-weight:700;line-height:1.35;text-transform:uppercase}
.assessment-card .status-badge{color:var(--state-color);background:var(--state-soft)}
.pill.danger{color:var(--red);background:var(--red-soft)}
.pill.warning{color:var(--amber);background:var(--amber-soft)}
.assessment-card h2{margin:15px 0 7px;font-size:clamp(26px,3vw,32px);line-height:1.2;letter-spacing:-.015em;overflow-wrap:break-word}
.assessment-lead{margin:0;color:#3f4c61;font-size:16px}
.assessment-scope{margin-top:17px;padding:13px 14px;border:1px solid var(--line);border-radius:10px;background:var(--surface)}
.assessment-scope>small,.next-action small,.assessment-chart>small{display:block;color:var(--muted);font-size:11px;font-weight:700;letter-spacing:.045em;text-transform:uppercase}
.scope-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px 18px;margin:9px 0 0}
.scope-grid>div{min-width:0}
.scope-grid .wide{grid-column:1/-1}
.scope-grid dt{color:var(--muted);font-size:11px;text-transform:uppercase}
.scope-grid dd{margin:1px 0 0;font-size:14px;font-weight:600;overflow-wrap:anywhere}
.assessment-scope .resource-id{margin:10px 0 0;padding-top:9px;border-top:1px solid var(--line)}
.assessment-scope .resource-id summary{font-size:12px}
.assessment-scope .resource-id code{display:block;margin-top:7px;color:var(--muted);font-size:12px;overflow-wrap:anywhere}
.next-action{margin-top:18px;padding-top:16px;border-top:1px solid var(--line)}
.next-action strong{display:block;margin-top:4px;font-size:17px;font-weight:600;overflow-wrap:break-word}
.trust-row{display:flex;flex-wrap:wrap;gap:8px 18px;margin-top:17px;color:var(--muted);font-size:12px}
.trust-row span{display:inline-flex;align-items:center;gap:7px}
.trust-row span:before{width:5px;height:5px;border-radius:50%;background:#718096;content:""}
.chart-value{margin-top:5px;font-size:28px;font-weight:700;letter-spacing:-.01em}
.chart-value span{color:var(--muted);font-size:14px;font-weight:600}
.assessment-chart p{margin:13px 0 0;color:var(--muted);font-size:12px}
.bullet{position:relative;height:92px;margin-top:18px}
.bullet .track{position:absolute;top:31px;width:100%;height:10px;border-radius:999px;background:linear-gradient(90deg,#bee7d2 0 65%,#f2d98f 65%)}
.marker{position:absolute;top:21px;width:3px;height:30px;background:#6d98ed}
.marker.p95{width:4px;background:var(--blue)}
.marker.target{background:#526174}
.marker span{position:absolute;left:50%;transform:translateX(-50%);white-space:nowrap;color:#34445f;font-size:11px;font-style:normal;font-weight:600}
.marker.p50 span{bottom:36px}
.marker.p95 span{top:38px}
.marker.target span{top:58px}
.chart-unavailable,.trend-unavailable{margin-top:16px;padding:11px 12px;border:1px solid #ead8a9;border-radius:8px;color:#654600;background:var(--card);font-size:12px}

.section-card{margin-top:16px;padding:22px}
.section-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;margin-bottom:17px}
.section-actions{display:flex;align-items:center;justify-content:flex-end;gap:8px}
.export-button{padding:8px 12px;border:1px solid #1d4ed8;border-radius:8px;color:#fff;background:var(--blue);cursor:pointer;font-size:12px;font-weight:700}
.export-button:hover{background:#1d4ed8}
.export-button:focus-visible{outline:3px solid #bfdbfe;outline-offset:2px}
.export-button:disabled{cursor:default;opacity:.7}
.section-heading h3{margin:0;font-size:20px;line-height:1.3;letter-spacing:-.01em}
.section-heading p{margin:3px 0 0;color:var(--muted);font-size:13px}
.group-heading{display:flex;justify-content:space-between;gap:14px;margin:19px 0 10px;color:var(--muted);font-size:12px}
.group-heading:first-of-type{margin-top:0}
.group-heading h4{margin:0;color:#40506a;font-size:12px;letter-spacing:.05em;text-transform:uppercase}
.latency-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:11px}
.latency-card,.profile-card{min-width:0;padding:15px;border:1px solid var(--line);border-radius:10px;background:var(--surface)}
.metric-head{display:flex;align-items:center;justify-content:space-between;gap:9px}
.metric-head strong{font-size:14px}
.info{position:relative;display:grid;flex:0 0 auto;place-items:center;width:20px;height:20px;padding:0;border:1px solid var(--line-strong);border-radius:50%;color:var(--muted);background:var(--card);cursor:help;font-size:12px;font-weight:700}
.info:hover:after,.info:focus:after{position:absolute;z-index:5;right:-8px;bottom:26px;width:230px;padding:9px;border:1px solid var(--line-strong);border-radius:8px;color:var(--ink);background:var(--card);box-shadow:var(--shadow);content:attr(data-help);font-size:12px;font-weight:400;text-align:left}
.metric-hero,.profile-main{margin-top:9px;font-size:24px;font-weight:700;letter-spacing:-.01em}
.metric-hero small,.profile-main small{color:var(--muted);font-size:11px;font-weight:600;letter-spacing:0}
.metric-pairs,.reliability{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin-top:10px}
.metric-pairs small,.reliability small{display:block;color:var(--muted);font-size:11px}
.metric-pairs strong,.reliability strong{display:block;font-weight:600}
.metric-source{margin-top:12px;padding-top:9px;border-top:1px solid var(--line);color:var(--muted);font-size:12px}
.workload-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:11px}
.profile-label{color:var(--muted);font-size:11px;font-weight:700;letter-spacing:.035em;text-transform:uppercase}
.profile-meta{display:flex;flex-wrap:wrap;justify-content:space-between;gap:4px 10px;margin-top:6px;color:var(--muted);font-size:12px}
.tpm-breakdown{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));margin-top:11px}
.tpm-breakdown>span{min-width:0;padding:0 8px 0 0}
.tpm-breakdown>span+span{padding-left:10px;border-left:1px solid var(--line)}
.tpm-breakdown small,.tpm-breakdown strong{display:block}
.tpm-breakdown small{color:var(--muted);font-size:11px;text-transform:uppercase}
.tpm-breakdown strong{margin-top:2px;font-size:12px;font-weight:600;overflow-wrap:anywhere}
.band-row{display:flex;gap:3px;margin-top:13px}
.band-row span{flex:1;min-width:0;padding:4px;background:var(--surface-strong);color:var(--muted);text-align:center;font-size:10px}
.band-row span:first-child{border-radius:6px 0 0 6px}
.band-row span:last-child{border-radius:0 6px 6px 0}
.band-row span.active{color:#174ea6;background:var(--blue-soft);font-weight:700}
.card-note{margin:10px 0 0;color:var(--muted);font-size:12px}
.rate-chart{display:grid;grid-template-columns:34px 1fr;align-items:center;gap:5px;margin-top:12px;color:var(--muted);font-size:11px}
.rate-chart i{height:7px;border-radius:999px;background:#e4e9f0}
.rate-chart b{display:block;height:100%;border-radius:inherit;background:#5f8fea}
.reliability span{padding:7px;border:1px solid var(--line);border-radius:7px;background:var(--card)}
.data-note,.safe-note{margin:14px 0 0;padding:10px 12px;border-left:3px solid #7aa3e8;border-radius:6px;color:#40506a;background:var(--surface);font-size:12px}
.data-note.warning{border-left-color:var(--amber);color:#654600;background:var(--surface)}

.recommendation-item small,.benchmark-stats small,.escalation-grid small{display:block;color:var(--muted);font-size:11px;font-weight:700;letter-spacing:.03em;text-transform:uppercase}
.recommendation-list{display:grid;gap:8px}
.recommendation-item{overflow:hidden;border:1px solid var(--line);border-left:3px solid #7aa3e8;border-radius:9px;background:var(--surface)}
.recommendation-body{display:grid;grid-template-columns:minmax(180px,.75fr) minmax(0,1fr) minmax(0,1.15fr);min-width:0}
.recommendation-pattern,.recommendation-evidence,.recommendation-action{min-width:0;padding:12px 14px}
.recommendation-pattern{display:flex;align-items:flex-start;flex-direction:column;justify-content:space-between;gap:8px}
.recommendation-pattern strong{display:block;margin-top:3px;font-size:15px}
.recommendation-confidence{flex:0 0 auto;padding:3px 7px;border-radius:999px;color:var(--green);background:var(--green-soft);font-size:10px;font-weight:700;text-transform:uppercase}
.recommendation-evidence,.recommendation-action{border-left:1px solid var(--line)}
.recommendation-item p{margin:3px 0 0;color:#40506a;font-size:13px}
.recommendation-action p{color:var(--ink);font-weight:600}
.benchmark-card{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:18px;align-items:center}
.benchmark-card h4{margin:0;font-size:15px}
.benchmark-card p{margin:5px 0;color:var(--muted);font-size:13px}
.benchmark-stats{display:grid;grid-template-columns:repeat(3,minmax(120px,1fr));gap:9px}
.benchmark-stats span,.escalation-grid span{padding:11px;border:1px solid var(--line);border-radius:9px;background:var(--surface)}
.benchmark-stats strong,.escalation-grid strong{display:block;margin-top:3px;font-weight:600}
.selected-cohort{margin-top:14px;padding:13px 14px;border:1px solid var(--line);border-left:3px solid #7aa3e8;border-radius:9px;background:var(--surface)}
.selected-cohort>small{display:block;color:#315f9f;font-size:11px;font-weight:700;letter-spacing:.04em;text-transform:uppercase}
.selected-cohort>strong{display:block;margin-top:4px;font-weight:600}
.cohort-chips{display:flex;flex-wrap:wrap;gap:6px;margin-top:9px}
.cohort-chips span{padding:4px 8px;border:1px solid var(--line);border-radius:999px;color:#40506a;background:var(--card);font-size:11px;font-weight:600}
.coverage-gap{padding:13px;border:1px solid #ead8a9;border-left:3px solid var(--amber);border-radius:9px;color:#654600;background:var(--surface)}
.coverage-gap p{margin:4px 0 0}
.escalation{border-color:var(--line);box-shadow:inset 4px 0 var(--red),var(--shadow)}
.evidence-preview{margin-top:0}
.escalation-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:9px}
.evidence-preview .escalation-grid{margin-top:12px}
.evidence-context{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:18px;margin-top:14px}
.evidence-context h4{margin:0 0 4px}
.evidence-context ul{margin:0;padding-left:20px}
.export-note{margin:13px 0 0;color:var(--muted);font-size:12px}
.evidence-package-data{display:none}
.setup{border-color:var(--line);box-shadow:inset 4px 0 var(--amber),var(--shadow)}
.setup-steps{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:9px;padding:0;counter-reset:step}
.setup-steps li{list-style:none;padding:12px;border:1px solid var(--line);border-radius:9px;background:var(--surface)}
.setup-steps li:before{display:block;color:var(--amber);content:counter(step);counter-increment:step;font-size:11px;font-weight:700}

details{margin-top:14px;border-top:1px solid var(--line);padding-top:11px}
summary{cursor:pointer;color:#34445f;font-weight:600}
.table-wrap{overflow-x:auto}
table{width:100%;border-collapse:collapse;margin-top:10px}
th,td{padding:9px 10px;border-bottom:1px solid var(--line);text-align:left}
th{color:var(--muted);font-size:11px;text-transform:uppercase}

.offer-section{margin-top:16px;overflow:hidden}
.offer-section>summary{display:flex;justify-content:space-between;margin:0;padding:19px 22px;border:0;list-style:none}
.offer-section>summary strong{font-size:17px}
.offer-section>summary em{color:var(--muted);font-style:normal;font-weight:400}
.offer-section>summary small{display:block;margin-top:2px;color:var(--muted)}
.offer-content{padding:0 22px 22px;border-top:1px solid var(--line)}
.offer-goals{margin:17px 0 0;padding:0;border:0}
.offer-goals legend{margin-bottom:10px;font-weight:700}
.offer-goal-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px}
.offer-goal{display:grid;grid-template-columns:20px minmax(0,1fr);align-items:start;gap:10px;min-width:0;padding:13px;border:1px solid var(--line-strong);border-radius:9px;background:var(--card);cursor:pointer}
.offer-goal:has(input:checked){border-color:#7ba1df;box-shadow:0 0 0 2px var(--blue-soft);background:var(--surface)}
.offer-goal input{width:16px;height:16px;margin:3px 0 0;accent-color:var(--blue)}
.offer-goal strong,.offer-goal small{display:block}
.offer-goal strong{font-size:14px}
.offer-goal small{margin-top:3px;color:var(--muted);font-size:12px}
.offer-note{margin-top:13px;padding:10px 12px;border-left:3px solid #7aa3e8;border-radius:6px;background:var(--surface);font-size:12px}
.offer-results{margin-top:14px}
.offer-result{display:none}
.offer-result.active{display:block}
.offer-card{display:flex;min-width:0;flex-direction:column;padding:16px;border:1px solid var(--line);border-radius:10px;background:var(--surface)}
.offer-card-heading{display:flex;align-items:center;justify-content:space-between;gap:12px}
.offer-card h4{margin:0;font-size:19px}
.offer-card-copy{margin-top:15px}
.offer-card-copy small{display:block;color:var(--muted);font-size:11px;font-weight:700;letter-spacing:.04em;text-transform:uppercase}
.offer-card-copy p{margin:4px 0 13px;color:#40506a}
.offer-card .safe-note{margin-top:2px}
footer{padding:20px;color:var(--muted);text-align:center;font-size:12px}

@media(max-width:980px){
  .assessment-card{grid-template-columns:1fr}
  .assessment-chart{border-top:1px solid var(--line);border-left:0}
  .workload-grid{grid-template-columns:repeat(2,minmax(0,1fr))}
  .benchmark-card{grid-template-columns:1fr}
  .escalation-grid,.setup-steps{grid-template-columns:repeat(2,minmax(0,1fr))}
}
@media(max-width:740px){
  .shell{width:min(calc(100% - 24px),1120px)}
  .topbar{padding:18px 0 21px}
  .topbar h1{font-size:27px}
  .latency-grid,.benchmark-stats,.offer-goal-grid{grid-template-columns:1fr}
  .assessment-copy,.assessment-chart,.section-card{padding:18px}
  .section-heading{display:block}
  .section-heading>.pill{margin-top:8px}
  .section-actions{justify-content:flex-start;margin-top:10px}
  .recommendation-body{grid-template-columns:1fr}
  .recommendation-evidence,.recommendation-action{border-top:1px solid var(--line);border-left:0}
  .offer-content{padding:0 16px 17px}
  .offer-card-heading{align-items:flex-start;flex-direction:column}
}
@media(max-width:520px){
  body{font-size:14px}
  main{padding-top:12px}
  .topbar h1{font-size:25px}
  .assessment-card h2{font-size:24px}
  .workload-grid,.escalation-grid,.setup-steps,.evidence-context,.scope-grid{grid-template-columns:1fr}
  .scope-grid .wide{grid-column:auto}
  .trust-row{display:grid;gap:6px}
  .scenario-picker>div{display:grid}
  .scenario-button{width:100%}
  .offer-card{grid-template-columns:1fr}
  .tpm-breakdown{grid-template-columns:1fr;gap:8px}
  .tpm-breakdown>span{padding:0}
  .tpm-breakdown>span+span{padding:8px 0 0;border-top:1px solid var(--line);border-left:0}
  .marker span{font-size:9px}
}
@media print{
  body{background:#fff}
  .scenario-picker,.offer-section{display:none}
  .card{box-shadow:none;break-inside:avoid}
}
`

const htmlScenarioInteraction = `for (const button of document.querySelectorAll(".scenario-button")) {
  button.addEventListener("click", () => {
    document.querySelectorAll(".scenario-button").forEach((item) => item.classList.toggle("active", item === button));
    document.querySelectorAll(".scenario-panel").forEach((panel) => panel.classList.toggle("active", panel.dataset.scenario === button.dataset.target));
  });
}
`

const htmlCommonInteraction = `for (const input of document.querySelectorAll(".offer-goal input")) {
  input.addEventListener("change", () => {
    const content = input.closest(".offer-content");
    content.querySelectorAll("[data-offer-result]").forEach((panel) =>
      panel.classList.toggle("active", panel.dataset.offerResult === input.value));
  });
}
for (const button of document.querySelectorAll("[data-export-evidence]")) {
  button.addEventListener("click", () => {
    const section = button.closest(".escalation");
    const data = section?.querySelector(".evidence-package-data")?.textContent;
    if (!data) return;
    const blob = new Blob([data.trim() + "\n"], {
      type: "application/json"
    });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = button.dataset.exportFilename ||
      "model-latency-evidence.json";
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
    const label = button.textContent;
    button.textContent = "Exported";
    button.disabled = true;
    setTimeout(() => {
      button.textContent = label;
      button.disabled = false;
    }, 1500);
  });
}
`

func htmlInteractionScript(hasScenarios bool) string {
	scenarioScript := ""
	if hasScenarios {
		scenarioScript = htmlScenarioInteraction + "\n"
	}
	return "\n\n<script>\n\n" + scenarioScript + htmlCommonInteraction + "</script>"
}
