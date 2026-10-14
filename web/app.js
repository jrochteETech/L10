'use strict';

const $ = (selector, root = document) => root.querySelector(selector);
const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const id = () => crypto.randomUUID ? crypto.randomUUID() : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
const now = () => new Date().toISOString();
const dateKey = date => `${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-${String(date.getDate()).padStart(2,'0')}`;
const today = () => dateKey(new Date());
const plusDays = days => { const d = new Date(); d.setDate(d.getDate()+days); return dateKey(d); };
const dateLabel = value => value ? new Date(value.length === 10 ? `${value}T12:00:00` : value).toLocaleDateString(undefined,{month:'short',day:'numeric',year:'numeric'}) : 'No due date';
const shortDate = value => new Date(`${value}T12:00:00`).toLocaleDateString(undefined,{month:'short',day:'numeric'});
const initials = name => name.split(/\s+/).filter(Boolean).slice(0,2).map(s=>s[0]).join('').toUpperCase();
const stages = [
  {name:'Segue', minutes:5, description:'Share one personal and one professional good thing. Connect as people before getting down to business.'},
  {name:'Scorecard', minutes:5, description:'Review your weekly measurables. Report the numbers, flag anything off track, and move the discussion to the Issues List.'},
  {name:'Rock review', minutes:5, description:'Each owner reports on track or off track on their quarterly priorities. Add off-track rocks to IDS.'},
  {name:'Headlines', minutes:5, description:'Share customer and employee headlines. Good news, concerns, and anything the team needs to know.'},
  {name:'To-do review', minutes:5, description:'Review last week’s commitments: done or not done. Aim for at least 90% completion.'},
  {name:'IDS', minutes:60, description:'Identify the root issue, discuss openly, and solve it. Start with your most important issues and turn decisions into accountable actions.'},
  {name:'Conclude', minutes:5, description:'Recap the to-dos, agree on cascading messages, and have each attendee rate this meeting from 1 to 10.'}
];
const navigation = [ ['overview','◈','Overview'], ['meeting','◷','L10 meeting'], ['scorecard','▥','Scorecard'], ['rocks','◇','Rocks'], ['todos','☑','To-dos'], ['issues','≡','Issues / IDS'], ['headlines','▤','Headlines'], ['history','↺','Meeting history'], ['settings','⚙','Team & settings'] ];
let state, currentUser, teams = [], users = [], setupRequired = false, passwordRequired = true, saving = false, saveQueue = Promise.resolve(), filter = 'open', search = '', selectedIssue = '', selectedHistory = '', toastTimeout;
const activeMeeting = () => state.meetings.find(m=>!m.endedAt);
const route = () => location.hash.slice(1).split('/')[0] || 'overview';
const memberName = (owner, members = state.members) => members.find(m=>m.id===owner)?.name || 'Unassigned';
const ownerHTML = (owner, members = state.members) => `<span class="owner"><span class="avatar">${esc(initials(memberName(owner,members))) || '—'}</span>${esc(memberName(owner,members))}</span>`;
const ownerOptions = (owner = '') => `<option value="">Unassigned</option>${state.members.map(m=>`<option value="${esc(m.id)}" ${owner===m.id?'selected':''}>${esc(m.name)}</option>`).join('')}`;
const pill = (text, color = '') => `<span class="pill ${color}">${esc(text)}</span>`;
const action = (name, label, item = '', style = 'secondary small') => `<button class="btn ${style}" data-action="${name}" data-id="${esc(item)}">${esc(label)}</button>`;
const editButtons = (type, item) => `<button class="icon-btn" data-action="edit-${type}" data-id="${esc(item)}" aria-label="Edit ${type}" title="Edit">✎</button>${type!=='member'?`<button class="icon-btn" data-action="move-${type}" data-id="${esc(item)}" aria-label="Move ${type} to another team" title="Move to another team">⇄</button>`:''}<button class="icon-btn" data-action="delete-${type}" data-id="${esc(item)}" aria-label="Delete ${type}" title="Delete">×</button>`;
const empty = (title, message, button = '') => `<div class="empty"><div class="empty-icon">◇</div><h3>${esc(title)}</h3><p>${esc(message)}</p>${button}</div>`;
const header = (title, description, button = '') => `<div class="page-head"><div><div class="eyebrow">Your weekly rhythm</div><h1>${esc(title)}</h1><p>${esc(description)}</p></div>${button}</div>`;

async function api(path, options = {}) {
  if(path==='state') path=`teams/${state.id}/state`;
  const response = await fetch(`/api/${path}`, { ...options, headers: {'Content-Type':'application/json', ...options.headers} });
  const data = await response.json();
  if (!response.ok) { const error = new Error(data.error || 'Request failed'); error.status = response.status; throw error; }
  return data;
}

function toast(message, error = false) {
  const box = $('#toast'); box.textContent = message; box.hidden = false; box.className = error ? 'error' : '';
  clearTimeout(toastTimeout); toastTimeout = setTimeout(()=>{box.hidden=true;},error?9000:4000);
}

function mutate(change, message = 'Changes saved') {
  const captured=captureDrafts();
  saveQueue = saveQueue.then(async () => {
    saving = true; updateSaveStatus();
    try {
      const next = structuredClone(state);
      applyDrafts(next,captured);
      change(next);
      state = await api(`teams/${next.id}/state`, {method:'PUT', body:JSON.stringify(next)});
      render(); if (message) toast(message);
      return true;
    } catch (error) {
      if (error.status === 409) { try { state = await api('state'); render(); } catch (_) {} }
      if (error.status === 401) { $('#modal').close(); renderLogin(); }
      toast(error.message, true); return false;
    } finally { saving = false; updateSaveStatus(); }
  });
  return saveQueue;
}

function updateSaveStatus() { const status = $('#save-status'); if (status) status.innerHTML = `<span class="dot"></span>${saving?'Saving…':'Saved to server'}`; }

function weeks() {
  const monday = new Date(); monday.setHours(12,0,0,0); monday.setDate(monday.getDate()-((monday.getDay()+6)%7));
  return Array.from({length:6},(_,i)=>{ const d=new Date(monday); d.setDate(d.getDate()-(5-i)*7); return dateKey(d); });
}
const currentWeek = () => weeks()[5];
const isOnTrack = (metric, value) => metric.direction === 'gte' ? value >= metric.goal : value <= metric.goal;
const completionRate = todos => todos.length ? Math.round(todos.filter(t=>t.done).length/todos.length*100) : 0;
const meetingAverage = m => { const scores = Object.values(m.ratings || {}); return scores.length ? (scores.reduce((a,b)=>a+b,0)/scores.length).toFixed(1) : '—'; };
const rockPill = status => status==='done'?pill('Complete'):status==='off-track'?pill('Off track','red'):pill('On track');

function render() {
  if (!state) { renderNoTeams(); return; }
  const current = navigation.find(n=>n[0]===route()) ? route() : 'overview';
  const views = {overview:renderOverview, meeting:renderMeeting, scorecard:()=>renderScorecard(), rocks:()=>renderRocks(), todos:()=>renderTodos(), issues:()=>renderIssues(), headlines:()=>renderHeadlines(), history:renderHistory, settings:renderSettings};
  $('#app').innerHTML = `<div class="layout"><aside class="sidebar"><a class="brand" href="#overview"><img class="brand-mark" src="/E%20Tech%20Group%20-%20Mark%20-%20Full%20Color%20-%20Background.png" alt="E Tech Group"><span>Level Ten<small>MAKE EVERY WEEK COUNT</small></span></a><div class="team-selector"><label for="team-select" class="team-label">Active team</label><select id="team-select" aria-label="Switch team">${teams.map(t=>`<option value="${esc(t.id)}" ${state.id===t.id?'selected':''}>${esc(t.name)}</option>`).join('')}</select>${currentUser.role==='admin'?'<button class="text-btn" data-action="create-team">+ Create team</button>':''}</div><nav class="nav" aria-label="Main navigation">${navigation.map(([key,icon,label])=>`<a href="#${key}" class="${key===current?'active':''}" ${key===current?'aria-current="page"':''}><span class="nav-icon" aria-hidden="true">${icon}</span>${label}${key==='issues' && state.issues.some(i=>i.status==='open')?`<span class="nav-count">${state.issues.filter(i=>i.status==='open').length}</span>`:''}${key==='meeting' && activeMeeting()?'<span class="nav-count">LIVE</span>':''}</a>`).join('')}</nav><div class="sidebar-bottom"><div class="team-card"><strong>${esc(currentUser.name)}</strong><small>${currentUser.role==='admin'?'Administrator':'Team member'} · ${teams.length} teams</small></div></div></aside><main class="main"><div class="topbar"><div class="breadcrumb">${esc(state.name)} <span>/ ${esc(navigation.find(n=>n[0]===current)[2])}</span></div><div class="top-actions"><span id="save-status" class="save-status"></span><button class="icon-btn" data-action="refresh" aria-label="Refresh workspace" title="Refresh workspace">↻</button><button class="text-btn" data-action="logout">Sign out</button><span class="avatar" title="${esc(currentUser.name)}">${esc(initials(currentUser.name))}</span></div></div><div class="content">${views[current]()}<div class="footer-note">LEVEL TEN · Clarity. Accountability. Traction.</div></div></main></div>`;
  decorateAssignments(); updateSaveStatus(); updateTimer();
}

function decorateAssignments() {
  for(const [type,collection] of Object.entries(collections)) {
    if(type==='member')continue;
    for(const item of state[collection]) {
      const ids=[...new Set([item.owner,...(item.assignees || [])].filter(Boolean))];if(!ids.length)continue;
      for(const button of document.querySelectorAll(`[data-action="edit-${type}"][data-id="${CSS.escape(item.id)}"]`)) {
        const row=button.closest('tr') || button.closest('.list-row');if(!row)continue;
        const title=$('.table-title',row) || $('.grow',row);if(!title)continue;
        const list=document.createElement('div');list.className='assigned-users';list.innerHTML=assignmentHTML(item);title.append(list);
      }
    }
  }
  if(route()==='history') {
    const reportMeeting=state.meetings.find(m=>m.id===selectedHistory && m.endedAt);
    if(reportMeeting) {
      const container=$('.page-head>.flex');if(container)container.insertAdjacentHTML('beforeend',action('move-meeting','⇄ Move report',reportMeeting.id));
    } else {
      for(const button of document.querySelectorAll('[data-action="view-history"].btn'))button.insertAdjacentHTML('afterend',action('move-meeting','⇄ Move',button.dataset.id));
    }
  }
}

function renderOverview() {
  const meeting = activeMeeting(), week = currentWeek(), measured = state.metrics.filter(m=>m.values[week]!==undefined), good = measured.filter(m=>isOnTrack(m,m.values[week])).length;
  const rocks = state.rocks.filter(r=>r.status!=='done'), todos=state.todos.filter(t=>!t.done), issues=state.issues.filter(i=>i.status==='open').sort((a,b)=>a.priority-b.priority);
  const last = state.meetings.filter(m=>m.endedAt).sort((a,b)=>b.startedAt.localeCompare(a.startedAt))[0];
  return `${header('A little structure. A lot of traction.',`${new Date().toLocaleDateString(undefined,{weekday:'long',month:'long',day:'numeric'})} · ${state.name}`,action(meeting?'resume':'start-meeting',meeting?'Resume meeting':'▶ Start L10 meeting','',''))}
    ${!state.members.length?`<div class="callout"><strong>Welcome to your meeting workspace.</strong> Start by adding your team, then define your weekly measurables and quarterly rocks. <a href="#settings">Set up your team →</a></div>`:''}
    <section class="hero"><div><div class="eyebrow">90 minutes. One focused team.</div><h2>${meeting?'Your meeting is in progress.':'Better meetings start here.'}</h2><p>${meeting?`You’re on ${esc(stages[meeting.stage].name)}. Keep the conversation focused and turn your biggest issues into clear next steps.`:'The same agenda, every week. Check the numbers, review your priorities, and spend the real time solving issues together.'}</p>${action(meeting?'resume':'start-meeting',meeting?'Continue your L10 →':'Let’s get started →','','lime')}<div class="agenda-preview">${stages.map(s=>`<span>${s.name} · ${s.minutes}m</span>`).join('')}</div></div><div class="hero-art"><div><strong>90</strong><small>MINUTES</small></div></div></section>
    <div class="stats"><div class="stat"><span class="label">Weekly scorecard</span><strong>${good}<span class="muted"> / ${state.metrics.length}</span></strong><small>${measured.length} reported · week of ${shortDate(week)}</small></div><div class="stat"><span class="label">Active rocks on track</span><strong>${rocks.filter(r=>r.status==='on-track').length}<span class="muted"> / ${rocks.length}</span></strong><small>Your quarterly priorities</small></div><div class="stat"><span class="label">Open to-dos</span><strong>${todos.length}</strong><small>${todos.filter(t=>t.due && t.due<today()).length} overdue commitments</small></div><div class="stat"><span class="label">Last meeting rating</span><strong>${last?meetingAverage(last):'—'}<span class="muted"> / 10</span></strong><small>${last?dateLabel(last.startedAt):'Your first L10 is ahead'}</small></div></div>
    <div class="grid two"><section class="card"><div class="card-head"><h2>What needs your attention</h2><a class="text-btn" href="#issues">View issues →</a></div>${issues.length?issues.slice(0,5).map(i=>`<div class="list-row"><span class="priority">P${i.priority}</span><div class="grow"><div class="row-title">${esc(i.title)}</div><div class="row-meta">${esc(memberName(i.owner))}</div></div>${pill('Open','gray')}</div>`).join(''):empty('A clear Issues List','Capture the obstacles, ideas, and opportunities your team needs to solve.',action('add-issue','Add an issue'))}</section><section class="card"><div class="card-head"><h2>This week’s commitments</h2><a class="text-btn" href="#todos">View to-dos →</a></div>${todos.length?todos.slice(0,5).map(t=>`<div class="list-row"><input class="check" type="checkbox" data-todo="${esc(t.id)}" aria-label="Complete ${esc(t.title)}"><div class="grow"><div class="row-title">${esc(t.title)}</div><div class="row-meta">${esc(memberName(t.owner))} · ${dateLabel(t.due)}</div></div>${t.due && t.due<today()?pill('Overdue','red'):''}</div>`).join(''):empty('Make the next step clear','Every action has one owner and a due date. Keep your commitments small and achievable.',action('add-todo','Add a to-do'))}</section></div>`;
}

function scorecardTable(metrics = state.metrics, editable = true, members = state.members, weekList = weeks()) {
  if (!metrics.length) return empty('Know your numbers','Add the weekly measurables that tell you whether the business is on track.',editable?action('add-metric','Add a measurable'):'');
  return `<div class="table-wrap"><table><thead><tr><th>Measurable / assignees</th><th>Weekly goal</th>${weekList.map((w,i)=>`<th class="${i===weekList.length-1?'week-current':''}">${shortDate(w)}</th>`).join('')}<th></th></tr></thead><tbody>${metrics.map(m=>`<tr><td class="table-title">${esc(m.name)}<div class="row-meta">${assignmentHTML(m,members)}</div></td><td>${m.direction==='gte'?'≥':'≤'} ${esc(m.goal)} ${esc(m.unit)}</td>${weekList.map(w=>{const value=m.values[w]; return `<td>${editable?`<input type="number" step="any" class="value-input ${value===undefined?'':isOnTrack(m,value)?'good':'bad'}" data-metric="${esc(m.id)}" data-week="${w}" value="${value===undefined?'':esc(value)}" aria-label="${esc(m.name)} week of ${shortDate(w)}" placeholder="—">`:value===undefined?'—':pill(`${value} ${m.unit}`,isOnTrack(m,value)?'':'red')}</td>`;}).join('')}<td><div class="flex">${editable?`${action('metric-issue','→ IDS',m.id)}${editButtons('metric',m.id)}`:''}</div></td></tr>`).join('')}</tbody></table></div>`;
}
function renderScorecard(embedded = false) {
  return `${embedded?'':header('Scorecard','A weekly pulse on your business. Report the numbers, then move on.',action('add-metric','+ Add measurable','',''))}<section class="card"><div class="card-head"><h2>Weekly measurables</h2>${embedded?action('add-metric','+ Add measurable'):pill(`Week of ${shortDate(currentWeek())}`,'gray')}</div>${scorecardTable()}</section><p class="hint">Weeks begin on Monday. Enter this week’s actuals directly in the table; changes save when you leave the field. Green meets the goal, red needs attention. Use “→ IDS” to create an issue.</p>`;
}

function rocksTable(rocks = state.rocks, editable = true, members = state.members) {
  if (!rocks.length) return empty('Focus on the big things','Define the most important outcomes for this quarter, with one accountable owner each.',editable?action('add-rock','Add a rock'):'');
  return `<div class="table-wrap"><table><thead><tr><th>Quarterly rock</th><th>Owner</th><th>Due date</th><th>Status</th><th></th></tr></thead><tbody>${rocks.map(r=>`<tr><td class="table-title">${esc(r.title)}</td><td>${ownerHTML(r.owner,members)}</td><td>${dateLabel(r.due)}</td><td>${editable?`<select class="input" data-rock="${esc(r.id)}" aria-label="Status for ${esc(r.title)}"><option value="on-track" ${r.status==='on-track'?'selected':''}>On track</option><option value="off-track" ${r.status==='off-track'?'selected':''}>Off track</option><option value="done" ${r.status==='done'?'selected':''}>Complete</option></select>`:rockPill(r.status)}</td><td><div class="flex">${editable?`${action('rock-issue','→ IDS',r.id)}${editButtons('rock',r.id)}`:''}</div></td></tr>`).join('')}</tbody></table></div>`;
}
function renderRocks(embedded = false) {
  const done=state.rocks.filter(r=>r.status==='done').length;
  return `${embedded?'':header('Rocks','The few priorities that matter most this quarter.',action('add-rock','+ Add rock','',''))}<section class="card"><div class="card-head"><h2>Quarterly priorities</h2>${embedded?action('add-rock','+ Add rock'):pill(`${done} of ${state.rocks.length} complete`,'gray')}</div>${rocksTable()}</section><p class="hint">On track or off track — keep the review short. If a rock needs a conversation, add it to the Issues List.</p>`;
}

function todosTable(todos = state.todos, editable = true, members = state.members) {
  if (!todos.length) return empty('Nothing here yet','Turn your decisions into seven-day commitments with a clear owner and due date.',editable?action('add-todo','Add a to-do'):'');
  return `<div class="table-wrap"><table><thead><tr><th>Commitment</th><th>Owner</th><th>Due date</th><th>Status</th><th></th></tr></thead><tbody>${todos.map(t=>`<tr><td class="table-title"><div class="flex">${editable?`<input class="check" type="checkbox" data-todo="${esc(t.id)}" ${t.done?'checked':''} aria-label="Complete ${esc(t.title)}">`:''}<span class="${t.done?'done-title':''}">${esc(t.title)}</span></div></td><td>${ownerHTML(t.owner,members)}</td><td>${dateLabel(t.due)}</td><td>${t.done?pill('Done'):t.due && t.due<today()?pill('Overdue','red'):pill('Open','gray')}</td><td>${editable?editButtons('todo',t.id):''}</td></tr>`).join('')}</tbody></table></div>`;
}
function renderTodos(embedded = false) {
  const list = embedded?state.todos.filter(t=>!t.done || t.completedAt.slice(0,10)>=plusDays(-7)):state.todos.filter(t=>filter==='all' || (filter==='done'?t.done:!t.done));
  return `${embedded?'':header('To-dos','One owner. One action. Seven days to follow through.',action('add-todo','+ Add to-do','',''))}${embedded?'':`<div class="toolbar"><div class="tabs">${[['open','Open'],['done','Completed'],['all','All to-dos']].map(([key,label])=>`<button class="tab ${filter===key?'active':''}" data-filter="${key}">${label}</button>`).join('')}</div><span class="muted subtitle">${state.todos.filter(t=>!t.done).length} open commitments</span></div>`}<section class="card"><div class="card-head"><h2>${embedded?'Weekly commitments':'Action list'}</h2>${embedded?action('add-todo','+ Add to-do'):pill(`${list.length} to-dos`,'gray')}</div>${todosTable(list)}</section>${embedded?`<p class="hint">Showing open to-dos and those completed in the last seven days. Completion: ${completionRate(list)}%. Target: 90% or better.</p>`:''}`;
}

function issuesList(issues, editable = true, members = state.members) {
  if (!issues.length) return empty('Make room for the real conversation','Capture issues here so you can stay focused during the rest of the agenda.',editable?action('add-issue','Add an issue'):'');
  return issues.map(i=>`<div class="list-row"><span class="priority">P${i.priority}</span><div class="grow"><div class="row-title">${esc(i.title)}</div><div class="row-meta">${esc(memberName(i.owner,members))}${i.details?' · Has discussion notes':''}</div></div>${editable?`<div class="flex">${i.status==='open'?action('select-issue','Discuss',i.id):action('reopen-issue','Reopen',i.id)}${editButtons('issue',i.id)}</div>`:pill(i.status==='solved'?'Solved':'Open',i.status==='solved'?'':'gray')}</div>`).join('');
}
function renderIssues(embedded = false) {
  const list=state.issues.filter(i=>(embedded?i.status==='open':filter==='all' || i.status===filter || (filter==='done' && i.status==='solved')) && (!search || `${i.title} ${i.details}`.toLowerCase().includes(search.toLowerCase()))).sort((a,b)=>a.priority-b.priority || a.createdAt.localeCompare(b.createdAt));
  const selected=state.issues.find(i=>i.id===selectedIssue && i.status==='open');
  return `${embedded?'':header('Issues / IDS','Solve the root cause. Leave with a decision and a next step.',action('add-issue','+ Add issue','',''))}${selected?`<section class="card"><div class="card-head"><h2>On the table</h2><button class="icon-btn" data-action="close-issue" aria-label="Close discussion">×</button></div><div class="card-body"><div class="ids-steps"><div><strong>1. Identify</strong><small>What’s the real issue underneath the symptoms?</small></div><div><strong>2. Discuss</strong><small>Get the facts and perspectives on the table.</small></div><div><strong>3. Solve</strong><small>Agree on a decision, an owner, and a next step.</small></div></div><h3>${esc(selected.title)}</h3><label class="field"><span>Root cause & discussion notes</span><textarea id="issue-discussion" rows="5">${esc(selected.details)}</textarea></label><div class="flex wrap">${action('save-discussion','Save notes',selected.id)}${action('solve-issue','Solve & assign action →',selected.id,'')} ${pill(`Priority ${selected.priority}`,'gray')}</div></div></section><div class="hint">Work one issue at a time. Prioritize importance over the order it was added.</div>`:''}
    ${embedded?'':`<div class="toolbar"><div class="tabs">${[['open','Open issues'],['done','Solved'],['all','All issues']].map(([key,label])=>`<button class="tab ${filter===key?'active':''}" data-filter="${key}">${label}</button>`).join('')}</div><input class="search" id="issue-search" placeholder="Search issues…" aria-label="Search issues" value="${esc(search)}"></div>`}<section class="card"><div class="card-head"><h2>${embedded?'Prioritize, then solve':'Issues List'}</h2>${embedded?action('add-issue','+ Add issue'):pill(`${list.length} issues`,'gray')}</div>${issuesList(list)}</section>`;
}

function headlinesList(headlines = state.headlines, editable = true) {
  if (!headlines.length) return empty('Keep your team in the loop','Share customer and employee news worth bringing to this week’s meeting.',editable?action('add-headline','Add a headline'):'');
  return headlines.map(h=>`<div class="list-row"><span class="avatar">${h.kind==='customer'?'C':'E'}</span><div class="grow"><div class="row-title">${esc(h.title)}</div><div class="row-meta">${h.kind==='customer'?'Customer':'Employee'} · ${dateLabel(h.createdAt)}</div></div>${editable?`<div class="flex">${action('headline-issue','→ IDS',h.id)}${editButtons('headline',h.id)}</div>`:''}</div>`).join('');
}
function renderHeadlines(embedded = false) {
  return `${embedded?'':header('Headlines','Customer and employee news your team needs to hear.',action('add-headline','+ Add headline','',''))}<section class="card"><div class="card-head"><h2>Customer & employee headlines</h2>${embedded?action('add-headline','+ Add headline'):pill(`${state.headlines.length} headlines`,'gray')}</div>${headlinesList()}</section><p class="hint">Keep each headline brief. Add anything that needs discussion to IDS. Headlines stay on the list until you remove them.</p>`;
}

function meetingSeconds(m, stage) {
  let seconds=m.stageSeconds[stage] || 0;
  if (m.stage===stage && m.timerStartedAt && !m.endedAt) seconds += Math.max(0,(Date.now()-Date.parse(m.timerStartedAt))/1000);
  return seconds;
}
const clock = seconds => { const s=Math.floor(Math.abs(seconds)); return `${seconds<0?'+':''}${String(Math.floor(s/60)).padStart(2,'0')}:${String(s%60).padStart(2,'0')}`; };
function updateTimer() {
  if (!state) return;
  const m=activeMeeting(), timer=$('#stage-timer'); if (!m || !timer) return;
  const remaining=stages[m.stage].minutes*60-meetingSeconds(m,m.stage);
  timer.textContent=clock(remaining); timer.classList.toggle('over',remaining<0);
  const total=$('#total-time'); if(total) total.textContent=`${clock(stages.reduce((sum,_,i)=>sum+meetingSeconds(m,i),0))} elapsed · 90:00 planned`;
}
function settleTimer(m) { if(m.timerStartedAt) { m.stageSeconds[m.stage] += Math.max(0,(Date.now()-Date.parse(m.timerStartedAt))/1000); m.timerStartedAt=''; } }

function renderMeeting() {
  const m=activeMeeting();
  if (!m) return `${header('Your Level 10 meeting','A consistent agenda. An accountable team. Every week.')}<section class="hero"><div><div class="eyebrow">The EOS Level 10 agenda</div><h2>Bring your team. Find your traction.</h2><p>A 90-minute meeting built around the things that matter: your numbers, your priorities, and the issues standing in your way.</p>${action('start-meeting','▶ Start a new meeting','','lime')}<div class="agenda-preview">${stages.map(s=>`<span>${s.name} · ${s.minutes}m</span>`).join('')}</div></div><div class="hero-art"><div><strong>10</strong><small>LEVEL UP</small></div></div></section><div class="card grid" style="">${empty('Come prepared','Before you begin, update your scorecard, review your rocks, and add the issues you want to solve.','<a class="btn secondary" href="#scorecard">Review scorecard →</a>')}</div>`;
  let body;
  if(m.stage===0) body=`<div class="stage-content"><h3>Who’s in the room?</h3><div class="attendees">${state.members.map(member=>`<label class="attendee"><input class="check" type="checkbox" data-attendee="${esc(member.id)}" ${m.attendees.includes(member.id)?'checked':''}>${esc(member.name)}</label>`).join('') || '<a class="text-btn" href="#settings">Add your team members →</a>'}</div><label class="field"><span>Good news & opening notes</span><textarea id="meeting-segue" rows="7" placeholder="One personal and one professional good thing from each person…">${esc(m.segue)}</textarea></label>${action('save-segue','Save opening notes')}</div>`;
  else if(m.stage===1) body=`<div class="stage-content">${renderScorecard(true)}</div>`;
  else if(m.stage===2) body=`<div class="stage-content">${renderRocks(true)}</div>`;
  else if(m.stage===3) body=`<div class="stage-content">${renderHeadlines(true)}</div>`;
  else if(m.stage===4) body=`<div class="stage-content">${renderTodos(true)}</div>`;
  else if(m.stage===5) body=`<div class="stage-content">${renderIssues(true)}<label class="field"><span>Meeting decisions & notes</span><textarea id="meeting-notes" rows="4" placeholder="Decisions, context, and anything to remember…">${esc(m.notes)}</textarea></label>${action('save-meeting-notes','Save meeting notes')}</div>`;
  else body=`<div class="stage-content"><div class="callout"><strong>Close the loop.</strong> Review the action owners and deadlines, decide what needs to be communicated, and rate the quality of this meeting. Discuss any rating below 8.</div><h3>Action recap</h3>${todosTable(state.todos.filter(t=>!t.done))}<label class="field"><span>Cascading messages</span><textarea id="meeting-messages" rows="3" placeholder="What needs to be shared, with whom, and by whom?">${esc(m.messages)}</textarea></label><label class="field"><span>Final meeting notes</span><textarea id="meeting-final-notes" rows="3">${esc(m.notes)}</textarea></label><h3>Rate this meeting</h3>${m.attendees.length?m.attendees.map(owner=>`<label class="rating-row">${ownerHTML(owner)}<select class="input" data-rating="${esc(owner)}" aria-label="Rating from ${esc(memberName(owner))}"><option value="">—</option>${Array.from({length:10},(_,i)=>`<option value="${i+1}" ${m.ratings[owner]===i+1?'selected':''}>${i+1}</option>`).join('')}</select></label>`).join(''):'<p class="hint">Select attendees in Segue to collect their ratings.</p>'}<div class="flex wrap">${action('save-conclusion','Save conclusion')}${pill(`Average: ${meetingAverage(m)} / 10`,'gray')}</div></div>`;
  return `${header('Let’s make this a Level 10.',`Started ${new Date(m.startedAt).toLocaleTimeString(undefined,{hour:'numeric',minute:'2-digit'})} · ${m.attendees.length} attendees`,pill('● Meeting in progress'))}<div class="meeting-layout"><aside class="card agenda" aria-label="Meeting agenda">${stages.map((stage,i)=>`<button data-stage="${i}" class="${m.stage===i?'active':''}" ${m.stage===i?'aria-current="step"':''}><span class="agenda-num">${i+1}</span><span><strong>${stage.name}</strong><small>${stage.minutes} minutes</small></span></button>`).join('')}<div class="timer-card"><div class="timer-label">Section time remaining</div><div id="stage-timer" class="timer">00:00</div>${action('toggle-timer',m.timerStartedAt?'Ⅱ Pause timer':'▶ Resume timer')}<div id="total-time" class="total-time"></div></div></aside><section class="card"><div class="stage-intro"><div class="eyebrow">Step ${m.stage+1} of 7 · ${stages[m.stage].minutes} minutes</div><h2>${stages[m.stage].name}</h2><p>${stages[m.stage].description}</p></div>${body}<div class="meeting-footer">${m.stage>0?action('previous-stage','← Previous'):pill('Start with connection','gray')}${m.stage<6?action('next-stage',`Next: ${stages[m.stage+1].name} →`,'',''):action('finish-meeting','Finish & save meeting ✓','','')}</div></section></div>`;
}

function report(m) {
  const snap=m.snapshot || state;
  const reportWeeks=(()=>{const d=new Date(m.startedAt);d.setDate(d.getDate()-((d.getDay()+6)%7));return Array.from({length:6},(_,i)=>{const w=new Date(d);w.setDate(w.getDate()-(5-i)*7);return dateKey(w);});})();
  return `${header(`Meeting · ${dateLabel(m.startedAt)}`,'A saved snapshot of your team’s meeting.',`<div class="flex">${action('back-history','← All meetings')}${action('print','Print report')}</div>`)}<section class="card"><div class="card-head"><h2>Meeting summary</h2>${pill('Concluded')}</div><div class="card-body"><div class="history-summary"><div><strong>${meetingAverage(m)} <small>/ 10</small></strong><small>Average rating</small></div><div><strong>${Math.round(m.stageSeconds.reduce((a,b)=>a+b,0)/60)} min</strong><small>Active meeting time</small></div><div><strong>${m.attendees.length}</strong><small>Attendees</small></div></div><p class="hint">${m.attendees.map(id=>esc(memberName(id,snap.members))).join(', ') || 'No attendees recorded'}</p><div class="agenda-preview">${stages.map((s,i)=>pill(`${s.name}: ${clock(m.stageSeconds[i])}`,'gray')).join('')}</div><div class="section-label">Opening / segue</div><div class="notes">${esc(m.segue)||'No opening notes recorded.'}</div><div class="section-label">Decisions & meeting notes</div><div class="notes">${esc(m.notes)||'No notes recorded.'}</div><div class="section-label">Cascading messages</div><div class="notes">${esc(m.messages)||'No cascading messages recorded.'}</div><div class="section-label">Individual ratings</div><div class="flex wrap">${m.attendees.map(id=>pill(`${memberName(id,snap.members)}: ${m.ratings[id] || '—'}`,'gray')).join('')}</div></div></section><div class="hint">This report preserves the workspace as it was when the meeting concluded.</div><div class="grid"><section class="card"><div class="card-head"><h2>Scorecard</h2></div>${scorecardTable(snap.metrics,false,snap.members,reportWeeks)}</section><section class="card"><div class="card-head"><h2>Rocks</h2></div>${rocksTable(snap.rocks,false,snap.members)}</section><section class="card"><div class="card-head"><h2>To-dos</h2></div>${todosTable(snap.todos,false,snap.members)}</section><div class="report-grid"><section class="card"><div class="card-head"><h2>Issues</h2></div>${issuesList(snap.issues,false,snap.members)}</section><section class="card"><div class="card-head"><h2>Headlines</h2></div>${headlinesList(snap.headlines,false)}</section></div></div>`;
}
function renderHistory() {
  const selected=state.meetings.find(m=>m.id===selectedHistory && m.endedAt); if(selected) return report(selected);
  const meetings=state.meetings.filter(m=>m.endedAt).sort((a,b)=>b.startedAt.localeCompare(a.startedAt));
  return `${header('Meeting history','A record of your decisions, commitments, and progress.')}<section class="card"><div class="card-head"><h2>Your weekly rhythm</h2>${pill(`${meetings.length} meetings`,'gray')}</div>${meetings.length?`<div class="table-wrap"><table><thead><tr><th>Meeting</th><th>Attendees</th><th>Active time</th><th>Rating</th><th></th></tr></thead><tbody>${meetings.map(m=>`<tr><td><button class="history-link" data-action="view-history" data-id="${esc(m.id)}">${dateLabel(m.startedAt)}</button><div class="row-meta">${new Date(m.startedAt).toLocaleTimeString(undefined,{hour:'numeric',minute:'2-digit'})}</div></td><td>${m.attendees.length} team members</td><td>${Math.round(m.stageSeconds.reduce((a,b)=>a+b,0)/60)} minutes</td><td>${pill(`${meetingAverage(m)} / 10`)}</td><td>${action('view-history','View report →',m.id)}</td></tr>`).join('')}</tbody></table></div>`:empty('Your progress starts here','Concluded meetings appear here with ratings, decisions, and a snapshot of your workspace.',action('start-meeting','Start your first L10'))}</section>`;
}

function renderSettings() {
  const admin=currentUser.role==='admin';
  return `${header('Team & settings','Manage team memberships, assignments, and your account.',admin?action('create-team','+ Create team','',''):'')}<div class="settings-grid"><section class="card"><div class="card-head"><h2>${esc(state.name)}</h2></div><div class="card-body"><form id="workspace-form">${field('name','Team name',state.name,'text','required maxlength="200"')}<button class="btn" type="submit">Save team name</button></form><div class="section-label">Move work between teams</div><p>Move individual items with ⇄, or move this team’s complete set of measurables, rocks, to-dos, issues, headlines, and concluded meetings.</p>${action('move-all','⇄ Move all items')}<p class="hint">Assignments are preserved. Missing assignees become members of the destination team. Conclude active meetings before a full transfer.</p></div></section><section class="card"><div class="card-head"><h2>Team members</h2>${admin?action('manage-members','Manage membership'):''}</div>${state.members.length?state.members.map(m=>`<div class="list-row"><span class="avatar">${esc(initials(m.name))}</span><div class="grow row-title">${esc(m.name)}</div>${pill('Member','gray')}</div>`).join(''):empty('No members assigned','An administrator can add user accounts to this team.')}</section><section class="card"><div class="card-head"><h2>Your account</h2></div><div class="card-body"><h3>${esc(currentUser.name)}</h3><p>${esc(currentUser.username)} · ${esc(currentUser.role)}</p>${action('change-password','Change my password')}</div></section>${admin?`<section class="card"><div class="card-head"><h2>User management</h2>${action('add-user','+ Add user')}</div>${users.map(u=>`<div class="list-row"><span class="avatar">${esc(initials(u.name))}</span><div class="grow"><div class="row-title">${esc(u.name)}</div><div class="row-meta">${esc(u.username)} · ${esc(u.role)} · ${teams.filter(t=>t.members.some(m=>m.id===u.id)).length} teams</div></div>${u.disabled?pill('Disabled','red'):''}${action('edit-user','Edit',u.id)}</div>`).join('')}</section><section class="card"><div class="card-head"><h2>Back up & restore</h2></div><div class="card-body"><p>Export all teams, accounts, password hashes, and meeting history. Keep backups private.</p><a class="btn secondary" href="/api/export" download="l10-backup.json">↓ Download backup</a><div class="section-label">Restore all data</div><label class="field"><span>Backup file (.json)</span><input class="file-input" id="backup-file" type="file" accept=".json,application/json"></label>${action('import','Restore backup','','danger')}</div></section>`:''}<section class="card"><div class="card-head"><h2>Your teams</h2></div>${teams.map(t=>`<div class="list-row"><div class="grow"><div class="row-title">${esc(t.name)}</div><div class="row-meta">${t.members.length} members</div></div><button class="btn secondary small" data-switch-team="${esc(t.id)}">${state.id===t.id?'Current team':'Open team →'}</button></div>`).join('')}</section></div>`;
}

function assigneesField(selected=[]) {
  return `<div class="field"><span>Assigned users</span><div class="attendees">${state.members.map(m=>`<label class="attendee"><input class="check" type="checkbox" name="assignee" value="${esc(m.id)}" ${selected.includes(m.id)?'checked':''}>${esc(m.name)}</label>`).join('') || '<small>Add users to this team in settings first.</small>'}</div><small>Select multiple collaborators. The primary accountable owner is always included.</small></div>`;
}
function assignmentHTML(item,members=state.members) {
  const ids=[...new Set([item.owner,...(item.assignees || [])].filter(Boolean))];
  return ids.length?ids.map(uid=>ownerHTML(uid,members)).join(' '):'<span class="muted">Unassigned</span>';
}
async function loadWorkspace(preferred) {
  teams=await api('teams');users=currentUser.role==='admin'?await api('users'):[];
  const chosen=teams.find(t=>t.id===(preferred || localStorage.getItem('l10-team'))) || teams[0];
  state=chosen?await api(`teams/${chosen.id}/state`):null;
  if(state)localStorage.setItem('l10-team',state.id);
}
async function switchTeam(teamID) {
  const captured=captureDrafts();if(state && Object.keys(captured.drafts).length){const ok=await mutate(next=>applyDrafts(next,captured),'');if(!ok)return;}
  await saveQueue;await loadWorkspace(teamID);selectedIssue='';selectedHistory='';search='';filter='open';render();
}
function renderNoTeams() {
  $('#app').innerHTML=`<div class="login"><div class="login-box"><h1>Welcome, ${esc(currentUser?.name)}.</h1><p>You don’t have a team yet. Ask an administrator to add you to a team.</p>${currentUser?.role==='admin'?action('create-team','Create a team'):''}${action('logout','Sign out')}</div></div>`;
}
function createTeam() {
  openModal('Create a team',field('name','Team name','','text','required maxlength="200" autofocus')+`<div class="field"><span>Team members</span><div class="attendees">${users.filter(u=>!u.disabled).map(u=>`<label class="attendee"><input class="check" type="checkbox" name="member" value="${esc(u.id)}" ${u.id===currentUser.id?'checked':''}>${esc(u.name)}</label>`).join('')}</div></div>`,async form=>{
    const team=await api('teams',{method:'POST',body:JSON.stringify({name:form.get('name').trim(),members:form.getAll('member')})});await loadWorkspace(team.id);render();toast('Team created');
  },'Create team');
}
function manageMembers() {
  if(currentUser.role!=='admin'){toast('An administrator manages team membership.',true);return;}
  openModal('Team membership',`<p class="muted">Users can belong to any number of teams. Removing a member clears their current assignments on this team; saved reports are preserved.</p><div class="attendees">${users.map(u=>`<label class="attendee"><input class="check" type="checkbox" name="member" value="${esc(u.id)}" ${state.members.some(m=>m.id===u.id)?'checked':''}>${esc(u.name)}${u.disabled?' (disabled)':''}</label>`).join('')}</div>`,async form=>{
    await saveQueue;state=await api(`teams/${state.id}/members`,{method:'PUT',body:JSON.stringify({members:form.getAll('member'),version:state.version})});await loadWorkspace(state.id);render();toast('Team membership saved');
  });
}
function editUser(userID='') {
  const u=users.find(u=>u.id===userID);
  openModal(u?'Edit user':'Create user',field('name','Full name',u?.name,'text','required maxlength="100"')+field('username','Username',u?.username,'text','required minlength="3" maxlength="80" pattern="[a-zA-Z0-9._@-]+"')+field('password',u?'New password (leave blank to keep current)':'Password','','password',`${u?'':'required'} minlength="10" maxlength="256" autocomplete="new-password"`)+`<label class="field"><span>Role</span><select name="role"><option value="member" ${u?.role!=='admin'?'selected':''}>Member — access assigned teams</option><option value="admin" ${u?.role==='admin'?'selected':''}>Administrator — manage all teams & users</option></select></label><label class="attendee"><input class="check" name="disabled" type="checkbox" ${u?.disabled?'checked':''}>Disable account</label><p class="hint">Usernames use letters, numbers, dots, dashes, underscores, or @. Passwords need at least 10 characters. Editing an account signs it out of all sessions.</p>`,async form=>{
    await api(u?`users/${u.id}`:'users',{method:u?'PUT':'POST',body:JSON.stringify({name:form.get('name'),username:form.get('username'),password:form.get('password'),role:form.get('role'),disabled:form.has('disabled')})});
    if(u?.id===currentUser.id){state=null;currentUser=null;renderLogin();toast('Account updated. Please sign in again.');return;}
    await loadWorkspace(state?.id);render();toast('User saved');
  });
}
function changePassword() {
  openModal('Change your password',field('current','Current password','','password','required autocomplete="current-password"')+field('password','New password','','password','required minlength="10" maxlength="256" autocomplete="new-password"'),async form=>{await api('password',{method:'POST',body:JSON.stringify(Object.fromEntries(form))});toast('Password changed. Other sessions have been signed out.');});
}
function moveDialog(type='',itemID='') {
  const other=teams.filter(t=>t.id!==state.id);if(!other.length){toast('You need access to another team before moving items.',true);return;}
  const collection=type==='meeting'?'meetings':collections[type];
  openModal(type?'Move item to another team':'Move all items to another team',`<p class="muted">${type?'Move this item':'Move all items and concluded meeting reports'} from ${esc(state.name)}. Assignments and data are preserved. Assigned users are automatically added to the destination team. Historical snapshots remain unchanged.</p><label class="field"><span>Destination team</span><select name="destination">${other.map(t=>`<option value="${esc(t.id)}">${esc(t.name)}</option>`).join('')}</select></label>`,async form=>{
    const captured=captureDrafts();if(Object.keys(captured.drafts).length) {const ok=await mutate(next=>applyDrafts(next,captured),'');if(!ok)return false;}
    await saveQueue;const destination=await api(`teams/${form.get('destination')}/state`);
    await api('transfer',{method:'POST',body:JSON.stringify({source:state.id,destination:destination.id,version:state.version,destinationVersion:destination.version,all:!type,items:type?{[collection]:[itemID]}:{}})});
    await loadWorkspace(state.id);render();toast('Items moved successfully');
  },'Move items');
}

function openModal(title, body, submit, label = 'Save') {
  const modal=$('#modal');
  modal.innerHTML=`<div class="modal-head"><h2 id="modal-title">${esc(title)}</h2><button class="icon-btn" type="button" data-close aria-label="Close dialog">×</button></div><form class="modal-body" id="modal-form">${body}<div id="modal-error" class="form-error" role="alert"></div><div class="modal-actions"><button type="button" class="btn secondary" data-close>Cancel</button><button type="submit" class="btn">${esc(label)}</button></div></form>`;
  if (!modal.open) modal.showModal();
  $('#modal-form').addEventListener('submit',async event=>{
    event.preventDefault(); const button=$('button[type="submit"]',modal); button.disabled=true;
    try { const result=await submit(new FormData(event.target)); if(result!==false) modal.close(); }
    catch(error) { $('#modal-error').textContent=error.message; }
    finally { button.disabled=false; }
  });
}
function field(name,label,value='',type='text',extra='') { return `<label class="field"><span>${esc(label)}</span><input name="${name}" type="${type}" value="${esc(value)}" ${extra}></label>`; }
function ownerField(owner='') { return `<label class="field"><span>Accountable owner</span><select name="owner">${ownerOptions(owner)}</select></label>`; }
function confirmDialog(title, message, callback, label='Confirm') { openModal(title,`<p class="muted">${esc(message)}</p>`,callback,label); }
const collections = {member:'members',metric:'metrics',rock:'rocks',todo:'todos',issue:'issues',headline:'headlines'};

function editItem(type,itemID='') {
  const item=state[collections[type]].find(i=>i.id===itemID), data=item || {};
  let body;
  if(type==='member') {manageMembers();return;}
  if(type==='metric') body=field('name','Measurable',data.name,'text','required maxlength="500" autofocus')+ownerField(data.owner)+`<div class="form-grid">${field('goal','Weekly goal',data.goal??0,'number','required step="any"')}<label class="field"><span>Goal direction</span><select name="direction"><option value="gte" ${data.direction!=='lte'?'selected':''}>At least (≥)</option><option value="lte" ${data.direction==='lte'?'selected':''}>At most (≤)</option></select></label></div>`+field('unit','Unit (optional)',data.unit,'text','maxlength="30" placeholder="e.g. $, %, calls"');
  if(type==='rock') body=field('title','Quarterly outcome',data.title,'text','required maxlength="500" autofocus')+ownerField(data.owner)+field('due','Due date',data.due||plusDays(90),'date')+`<label class="field"><span>Status</span><select name="status"><option value="on-track" ${data.status==='on-track'?'selected':''}>On track</option><option value="off-track" ${data.status==='off-track'?'selected':''}>Off track</option><option value="done" ${data.status==='done'?'selected':''}>Complete</option></select></label>`;
  if(type==='todo') body=field('title','Next action',data.title,'text','required maxlength="500" autofocus')+ownerField(data.owner)+field('due','Due date',data.due||plusDays(7),'date','required');
  if(type==='issue') body=field('title','What’s the issue?',data.title,'text','required maxlength="500" autofocus')+ownerField(data.owner)+`<label class="field"><span>Priority</span><select name="priority">${[1,2,3].map(p=>`<option value="${p}" ${(data.priority||2)===p?'selected':''}>${p} · ${['Highest','Normal','Lower'][p-1]}</option>`).join('')}</select></label><label class="field"><span>Context & discussion notes</span><textarea name="details" rows="4" maxlength="20000">${esc(data.details)}</textarea></label>`;
  if(type==='headline') body=field('title','Headline',data.title,'text','required maxlength="500" autofocus')+`<label class="field"><span>Category</span><select name="kind"><option value="customer" ${data.kind!=='employee'?'selected':''}>Customer</option><option value="employee" ${data.kind==='employee'?'selected':''}>Employee</option></select></label>`;
  body+=assigneesField(data.assignees || (data.owner?[data.owner]:[]));
  openModal(`${item?'Edit':'Add'} ${type==='metric'?'measurable':type==='todo'?'to-do':type}`,body,async form=>{
    const values=Object.fromEntries(form); for(const key of ['name','title']) if(key in values) { values[key]=values[key].trim(); if(!values[key]) throw new Error('Please enter a name or title.'); }
    delete values.assignee; values.assignees=form.getAll('assignee');
    if(values.owner && !values.assignees.includes(values.owner))values.assignees.unshift(values.owner);
    if(type==='metric') { values.goal=Number(values.goal); if(!Number.isFinite(values.goal)) throw new Error('Enter a valid goal.'); }
    if(type==='issue') values.priority=Number(values.priority);
    return mutate(next=>{
      const list=next[collections[type]], current=list.find(i=>i.id===itemID);
      if(item && !current) throw new Error('This item was removed. Please refresh and try again.');
      if(current) Object.assign(current,values);
      else { const defaults={member:{},metric:{values:{}},rock:{},todo:{done:false,completedAt:''},issue:{status:'open',createdAt:now()},headline:{createdAt:now()}}; list.push({id:id(),...defaults[type],...values}); }
    });
  });
}

function deleteItem(type,itemID) {
  if(type==='member') {manageMembers();return;}
  const item=state[collections[type]].find(i=>i.id===itemID); if(!item) return;
  confirmDialog(`Delete ${type==='todo'?'to-do':type}?`,type==='member'?`Remove ${item.name} from the team? Current ownership will be unassigned. Saved meeting reports keep their original attendees and owners.`:`Remove “${item.title || item.name}” from the workspace? Saved meeting reports keep their snapshot.`,()=>mutate(next=>{
    next[collections[type]]=next[collections[type]].filter(i=>i.id!==itemID);
    if(type==='member') {
      for(const key of ['metrics','rocks','todos','issues']) for(const entry of next[key]) if(entry.owner===itemID) entry.owner='';
      for(const m of next.meetings.filter(m=>!m.endedAt)) { m.attendees=m.attendees.filter(a=>a!==itemID); delete m.ratings[itemID]; }
    }
  },'Item removed'),'Delete');
}

function startMeeting() {
  if(activeMeeting()) { location.hash='meeting'; render(); return; }
  openModal('Start your Level 10',`<p class="muted">Your 90-minute agenda starts with Segue. Select the team members attending today.</p><div class="attendees">${state.members.map(m=>`<label class="attendee"><input class="check" type="checkbox" name="attendees" value="${esc(m.id)}" checked>${esc(m.name)}</label>`).join('') || 'No team members yet. You can add them in Team & settings.'}</div>`,async form=>{
    const attendees=form.getAll('attendees');
    const ok=await mutate(next=>{
      if(next.meetings.some(m=>!m.endedAt)) throw new Error('A meeting is already in progress. Refresh to resume it.');
      next.meetings.push({id:id(),startedAt:now(),endedAt:'',stage:0,stageSeconds:[0,0,0,0,0,0,0],timerStartedAt:now(),attendees,segue:'',notes:'',messages:'',ratings:{},snapshot:null});
    },'Meeting started. Let’s connect.');
    if(ok) { location.hash='meeting'; render(); } return ok;
  },'Start meeting');
}

function captureDrafts() {
  const drafts={};
  for(const [selector,key] of [['#meeting-segue','segue'],['#meeting-notes','notes'],['#meeting-messages','messages'],['#meeting-final-notes','notes']]) { const el=$(selector); if(el) drafts[key]=el.value; }
  if($('#issue-discussion')) drafts.issueDetails=$('#issue-discussion').value;
  return {drafts,issueID:selectedIssue};
}
function applyDrafts(next, captured) {
  const meeting=next.meetings.find(m=>!m.endedAt);
  if(meeting) for(const key of ['segue','notes','messages']) if(key in captured.drafts) meeting[key]=captured.drafts[key];
  if('issueDetails' in captured.drafts) { const issue=next.issues.find(i=>i.id===captured.issueID); if(issue) issue.details=captured.drafts.issueDetails; }
}
function changeStage(stage) {
  const captured=captureDrafts();
  mutate(next=>{ const m=next.meetings.find(m=>!m.endedAt); if(!m) throw new Error('No active meeting.'); applyDrafts(next,captured); const running=!!m.timerStartedAt; settleTimer(m); m.stage=stage; if(running)m.timerStartedAt=now(); },'');
}
function sendToIDS(type,itemID) {
  const item=state[collections[type]].find(i=>i.id===itemID); if(!item)return;
  const title=type==='metric'?`Off-track measurable: ${item.name}`:type==='rock'?`Rock needs attention: ${item.title}`:item.title;
  openModal('Add to Issues List',field('title','Issue',title,'text','required maxlength="500"')+ownerField(item.owner)+assigneesField(item.assignees || [])+`<label class="field"><span>Context</span><textarea name="details" rows="3" maxlength="20000" placeholder="What needs to be identified and solved?"></textarea></label>`,form=>mutate(next=>{
    next.issues.push({id:id(),title:form.get('title').trim(),owner:form.get('owner'),assignees:[...new Set([form.get('owner'),...form.getAll('assignee')].filter(Boolean))],details:form.get('details'),priority:1,status:'open',createdAt:now()});
  },'Added to IDS'));
}
function solveIssue(itemID) {
  const issue=state.issues.find(i=>i.id===itemID); if(!issue)return;
  const discussion=$('#issue-discussion')?.value ?? issue.details;
  openModal('Solve this issue',`<p class="muted">${esc(issue.title)}</p><label class="field"><span>Decision / resolution</span><textarea name="resolution" required rows="3" maxlength="10000" placeholder="What did the team decide?"></textarea></label>${field('action','Next action (optional)','','text','maxlength="500" placeholder="A specific seven-day commitment"')}${ownerField(issue.owner)}${assigneesField(issue.assignees || [])}${field('due','Action due date',plusDays(7),'date','required')}`,async form=>{
    if(!form.get('resolution').trim()) throw new Error('Record the team’s decision.');
    if(form.get('action').trim() && !form.get('owner')) throw new Error('Choose an owner for the next action.');
    const ok=await mutate(next=>{
      const current=next.issues.find(i=>i.id===itemID); if(!current)throw new Error('Issue no longer exists.');
      current.details=`${discussion}${discussion?'\n\n':''}Resolution (${today()}): ${form.get('resolution').trim()}`;
      current.status='solved';
      if(form.get('action').trim())next.todos.push({id:id(),title:form.get('action').trim(),owner:form.get('owner'),assignees:[...new Set([form.get('owner'),...form.getAll('assignee')].filter(Boolean))],due:form.get('due'),done:false,completedAt:''});
    },'Issue solved. Keep the momentum.');
    if(ok) { selectedIssue=''; render(); } return ok;
  },'Mark solved');
}
function finishMeeting() {
  const captured=captureDrafts(), m=activeMeeting(); if(!m)return;
  const missing=m.attendees.filter(id=>!m.ratings[id]).length;
  confirmDialog('Conclude your meeting?',`Save the meeting report with all notes, ratings, and a snapshot of the workspace.${missing?` ${missing} attendee${missing===1?' has':'s have'} not submitted a rating.`:''} Your rocks, scorecard, to-dos, and open issues carry forward to next week.`,async()=>{
    const ok=await mutate(next=>{
      const meeting=next.meetings.find(m=>!m.endedAt); if(!meeting)throw new Error('Meeting already concluded.');
      applyDrafts(next,captured); settleTimer(meeting); meeting.endedAt=now();
      meeting.snapshot=Object.fromEntries(['members','metrics','rocks','todos','issues','headlines'].map(key=>[key,structuredClone(next[key])]));
      selectedHistory=meeting.id;
    },'Meeting saved. See you next week.');
    if(ok) {location.hash='history';render();}return ok;
  },'Finish & save');
}

const handlers = {
  'start-meeting':startMeeting, resume:()=>{location.hash='meeting';render();},
  refresh:async()=>{await saveQueue;try{await loadWorkspace(state?.id);render();toast('Workspace refreshed');}catch(e){toast(e.message,true);}},
  logout:async()=>{try{await api('logout',{method:'POST',body:'{}'});state=null;renderLogin();}catch(e){toast(e.message,true);}},
  'toggle-timer':()=>{const captured=captureDrafts();mutate(next=>{applyDrafts(next,captured);const m=next.meetings.find(m=>!m.endedAt);if(!m)throw new Error('No active meeting.');if(m.timerStartedAt)settleTimer(m);else m.timerStartedAt=now();},'');},
  'previous-stage':()=>changeStage(Math.max(0,activeMeeting().stage-1)), 'next-stage':()=>changeStage(Math.min(6,activeMeeting().stage+1)),
  'save-segue':()=>{const captured=captureDrafts();mutate(next=>applyDrafts(next,captured));},
  'save-meeting-notes':()=>{const captured=captureDrafts();mutate(next=>applyDrafts(next,captured));},
  'save-conclusion':()=>{const captured=captureDrafts();mutate(next=>applyDrafts(next,captured));},
  'finish-meeting':finishMeeting,
  'metric-issue':item=>sendToIDS('metric',item), 'rock-issue':item=>sendToIDS('rock',item), 'headline-issue':item=>sendToIDS('headline',item),
  'select-issue':item=>{selectedIssue=item;render();}, 'close-issue':()=>{selectedIssue='';render();},
  'save-discussion':item=>{const value=$('#issue-discussion').value;mutate(next=>{const i=next.issues.find(i=>i.id===item);if(i)i.details=value;});},
  'solve-issue':solveIssue, 'reopen-issue':item=>mutate(next=>{const i=next.issues.find(i=>i.id===item);if(i)i.status='open';}),
  'view-history':item=>{selectedHistory=item;render();}, 'back-history':()=>{selectedHistory='';render();}, print:()=>window.print(),
  'create-team':createTeam, 'manage-members':manageMembers, 'add-user':()=>editUser(), 'edit-user':editUser,
  'move-all':()=>moveDialog(), 'change-password':changePassword,
  import:async()=>{
    const file=$('#backup-file').files[0];if(!file){toast('Choose a backup JSON file first.',true);return;}
    if(file.size>8*1024*1024){toast('Backup must be smaller than 8 MB.',true);return;}
    let backup;try{backup=JSON.parse(await file.text());if(!backup || !Array.isArray(backup.teams) || !Array.isArray(backup.users))throw new Error();}catch(_){toast('That file is not a valid multi-team backup.',true);return;}
    const current=await api('export');
    confirmDialog('Restore all teams and accounts?',`Replace all current teams, users, and meeting history with this backup (${backup.teams.length} teams)? Download a backup first. You will need to sign in with an account from the restored backup.`,async()=>{await api('import',{method:'POST',body:JSON.stringify({version:current.version,database:backup})});state=null;currentUser=null;setupRequired=false;renderLogin();toast('Backup restored. Sign in again.');},'Restore all data');
  }
};

document.addEventListener('click',event=>{
  const switchButton=event.target.closest('[data-switch-team]');if(switchButton){switchTeam(switchButton.dataset.switchTeam).catch(e=>toast(e.message,true));return;}
  if(event.target.closest('[data-close]')){$('#modal').close();return;}
  const button=event.target.closest('[data-action]');
  if(button){
    const name=button.dataset.action,item=button.dataset.id;
    if(handlers[name])Promise.resolve(handlers[name](item)).catch(e=>toast(e.message,true));
    else if(name.startsWith('add-'))editItem(name.slice(4));
    else if(name.startsWith('edit-'))editItem(name.slice(5),item);
    else if(name.startsWith('delete-'))deleteItem(name.slice(7),item);
    else if(name.startsWith('move-'))moveDialog(name.slice(5),item);
  }
  const stage=event.target.closest('[data-stage]');if(stage)changeStage(Number(stage.dataset.stage));
  const tab=event.target.closest('[data-filter]');if(tab){filter=tab.dataset.filter;render();}
});
document.addEventListener('change',event=>{
  if(event.target.id==='team-select'){switchTeam(event.target.value).catch(e=>toast(e.message,true));return;}
  const target=event.target, captured=captureDrafts();
  if(target.dataset.metric){const value=target.value;mutate(next=>{applyDrafts(next,captured);const m=next.metrics.find(m=>m.id===target.dataset.metric);if(!m)return;if(value==='')delete m.values[target.dataset.week];else {const number=Number(value);if(!Number.isFinite(number))throw new Error('Enter a valid number.');m.values[target.dataset.week]=number;}},'');}
  if(target.dataset.rock)mutate(next=>{applyDrafts(next,captured);const r=next.rocks.find(r=>r.id===target.dataset.rock);if(r)r.status=target.value;},'');
  if(target.dataset.todo){const checked=target.checked;mutate(next=>{applyDrafts(next,captured);const t=next.todos.find(t=>t.id===target.dataset.todo);if(t){t.done=checked;t.completedAt=checked?now():'';}},'');}
  if(target.dataset.attendee){const checked=target.checked,member=target.dataset.attendee;mutate(next=>{applyDrafts(next,captured);const m=next.meetings.find(m=>!m.endedAt);if(!m)return;if(checked && !m.attendees.includes(member))m.attendees.push(member);else if(!checked){m.attendees=m.attendees.filter(id=>id!==member);delete m.ratings[member];}},'');}
  if(target.dataset.rating){const value=target.value,member=target.dataset.rating;mutate(next=>{applyDrafts(next,captured);const m=next.meetings.find(m=>!m.endedAt);if(!m)return;if(value)m.ratings[member]=Number(value);else delete m.ratings[member];},'');}
});
document.addEventListener('input',event=>{
  if(event.target.id==='issue-search'){const position=event.target.selectionStart;search=event.target.value;render();const input=$('#issue-search');input.focus();input.setSelectionRange(position,position);}
});
document.addEventListener('submit',event=>{
  if(event.target.id==='workspace-form'){event.preventDefault();const name=new FormData(event.target).get('name').trim();if(!name){toast('Enter a workspace name.',true);return;}mutate(next=>{next.name=name;});}
});
window.addEventListener('hashchange',async()=>{const captured=captureDrafts();if(state && Object.keys(captured.drafts).length){const ok=await mutate(next=>applyDrafts(next,captured),'');if(!ok)return;}filter='open';search='';selectedIssue='';if(route()!=='history')selectedHistory='';if(currentUser && state)render();window.scrollTo(0,0);});
window.addEventListener('beforeunload',event=>{
  const captured=captureDrafts(),m=state && activeMeeting();
  const dirty=m && ['segue','notes','messages'].some(key=>key in captured.drafts && captured.drafts[key]!==m[key]);
  const issue=state?.issues.find(i=>i.id===captured.issueID);
  if(saving || dirty || (issue && 'issueDetails' in captured.drafts && captured.drafts.issueDetails!==issue.details)){event.preventDefault();event.returnValue='';}
});
setInterval(updateTimer,1000);

function renderLogin() {
  state=null;
  $('#app').innerHTML=`<div class="login"><div class="login-box"><div class="brand"><img class="brand-mark" src="/E%20Tech%20Group%20-%20Mark%20-%20Full%20Color%20-%20Background.png" alt="E Tech Group"><span>Level Ten<small>MAKE EVERY WEEK COUNT</small></span></div><h1>${setupRequired?'Set up your workspace.':'Welcome back.'}</h1><p>${setupRequired?'Create the first administrator account to get started.':'Sign in to your team’s meeting workspace.'}</p><form id="login-form">${setupRequired?field('name','Your name','','text','required maxlength="100" autocomplete="name"'):''}${field('username','Username','','text','required minlength="3" maxlength="80" autocomplete="username"')}${field('password','Password','','password',`required ${setupRequired?'minlength="10" autocomplete="new-password"':'autocomplete="current-password"'} maxlength="256"`)}<div id="login-error" class="form-error" role="alert"></div><button class="btn" type="submit">${setupRequired?'Create administrator →':'Sign in →'}</button></form></div></div>`;
  $('#login-form').addEventListener('submit',async event=>{
    event.preventDefault();const button=$('button',event.target);button.disabled=true;
    try{currentUser=await api(setupRequired?'setup':'login',{method:'POST',body:JSON.stringify(Object.fromEntries(new FormData(event.target)))});setupRequired=false;await loadWorkspace();render();}
    catch(error){$('#login-error').textContent=error.message;}finally{button.disabled=false;}
  });
}
async function init() {
  try {const session=await api('session');setupRequired=session.setupRequired;currentUser=session.user;if(!session.authenticated){renderLogin();return;}await loadWorkspace();render();}
  catch(error){$('#app').innerHTML=`<div class="loading"><h1>Couldn’t load your workspace</h1><p>${esc(error.message)}</p><button class="btn" id="retry">Try again</button></div>`;$('#retry').addEventListener('click',init);}
}
init();
