const zh = navigator.language.toLowerCase().startsWith('zh')
const T = zh
  ? { name: '这个 Chrome 叫', namePh: '比如 工作、个人', nameHint: '告诉 agent「用工作那个 Chrome」时，它按这个名字找。', on: '已连接', off: '未连接', offHint: '打开 Gerdr 后会自动连上。', lastDrop: '上次断开', logHint: '还连不上的话，Gerdr 数据目录下 logs/browser-host.log 里有原因。', onHint: 'Gerdr 只会在以下您授权过的网站中操作。', sites: '允许的网站', none: '还没有。Gerdr 第一次要用某个网站时会先问你。', remove: '移除' }
  : { name: 'Name this Chrome', namePh: 'e.g. Work, Personal', nameHint: 'Tell an agent "use the Work Chrome" and it finds this one by name.', on: 'Connected', off: 'Not connected', offHint: 'Connects automatically when Gerdr is running.', lastDrop: 'Last dropped', logHint: "Still not connecting? Gerdr's data dir has the reason in logs/browser-host.log.", onHint: "Gerdr only works on the sites below that you've allowed.", sites: 'Allowed sites', none: 'None yet. Gerdr asks the first time it needs a site.', remove: 'Remove' }
document.getElementById('sitesTitle').textContent = T.sites
document.getElementById('nameTitle').textContent = T.name
document.getElementById('nameHint').textContent = T.nameHint

const nameInput = document.getElementById('name')
nameInput.placeholder = T.namePh
chrome.storage.local.get('name').then(({ name }) => (nameInput.value = name || ''))
// saved on every keystroke: the popup can close at any moment, without a blur
nameInput.addEventListener('input', () => chrome.storage.local.set({ name: nameInput.value.trim() }))
nameInput.addEventListener('keydown', (e) => e.key === 'Enter' && nameInput.blur())

async function render() {
  // asking for the status also reconnects (background.js), so this can take up to 2 s
  const { connected, lastDrop } = await chrome.runtime.sendMessage({ type: 'status' }).catch(() => ({ connected: false }))
  const state = document.getElementById('state')
  state.textContent = connected ? T.on : T.off
  state.className = 'badge ' + (connected ? 'on' : 'off')
  document.getElementById('hint').textContent = connected ? T.onHint : T.offHint
  // why it isn't connected: what Chrome said when the last connection ended, and where the host's side is
  const drop = document.getElementById('drop')
  drop.textContent = ''
  if (!connected && lastDrop) {
    const when = new Date(lastDrop.at).toLocaleString()
    drop.textContent = `${T.lastDrop}: ${lastDrop.why || '—'} (${when}). ${T.logHint}`
  }
  const p = await chrome.permissions.getAll()
  const origins = (p.origins || []).filter((o) => o !== 'https://*/*' && o !== 'http://*/*')
  const ul = document.getElementById('sites')
  ul.textContent = ''
  if (!origins.length) {
    const li = document.createElement('li')
    li.className = 'muted'
    li.textContent = T.none
    ul.append(li)
  }
  for (const o of origins) {
    const li = document.createElement('li')
    const name = document.createElement('span')
    name.textContent = o.replace(/\/\*$/, '').replace(/^https?:\/\//, '')
    const btn = document.createElement('button')
    btn.textContent = T.remove
    btn.onclick = async () => {
      await chrome.permissions.remove({ origins: [o] })
      render()
    }
    li.append(name, btn)
    ul.append(li)
  }
}
render()
