const zh = navigator.language.toLowerCase().startsWith('zh')
const T = zh
  ? { on: '已连接', off: '未连接', offHint: '打开 Gerdr 后会自动连上。', onHint: 'Gerdr 只会在以下您授权过的网站中操作。', sites: '允许的网站', none: '还没有。Gerdr 第一次要用某个网站时会先问你。', remove: '移除' }
  : { on: 'Connected', off: 'Not connected', offHint: 'Connects automatically when Gerdr is running.', onHint: "Gerdr only works on the sites below that you've allowed.", sites: 'Allowed sites', none: 'None yet. Gerdr asks the first time it needs a site.', remove: 'Remove' }
document.getElementById('sitesTitle').textContent = T.sites

async function render() {
  const { connected } = await chrome.runtime.sendMessage({ type: 'status' }).catch(() => ({ connected: false }))
  const state = document.getElementById('state')
  state.textContent = connected ? T.on : T.off
  state.className = 'badge ' + (connected ? 'on' : 'off')
  document.getElementById('hint').textContent = connected ? T.onHint : T.offHint
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
