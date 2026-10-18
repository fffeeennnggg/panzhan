// Reconcile live data without replacing focused controls or restarting their lifecycle.
const nodeKey = node => node.nodeType === 1 ? node.getAttribute('data-dom-key') || node.id ||
 (node.hasAttribute('data-search-option') ? 'filter:' + node.getAttribute('data-search-option') :
 node.hasAttribute('data-task') ? 'task-button:' + node.getAttribute('data-task') :
 node.hasAttribute('data-job-action') ? node.getAttribute('data-job-action') + ':' + node.getAttribute('data-id') :
 node.hasAttribute('data-action') ? 'action:' + node.getAttribute('data-action') : '') : '';

function compatible(a, b) {
 return a.nodeType === b.nodeType && a.nodeName === b.nodeName && nodeKey(a) === nodeKey(b);
}

function patchNode(current, next) {
 if (current.nodeType !== 1) {
  if (current.nodeValue !== next.nodeValue) current.nodeValue = next.nodeValue;
  return;
 }
 const active = current.ownerDocument.activeElement === current;
 for (const attr of [...current.attributes]) {
  if (current.nodeName === 'DETAILS' && attr.name === 'open') continue;
  if (!next.hasAttribute(attr.name)) current.removeAttribute(attr.name);
 }
 for (const attr of next.attributes) {
  if (current.nodeName === 'DETAILS' && attr.name === 'open') continue;
  if (current.getAttribute(attr.name) !== attr.value) current.setAttribute(attr.name, attr.value);
 }
 // Updating a native select's options while it is open dismisses its popup.
 if (active && current.nodeName === 'SELECT') return;
 patchChildren(current, next);
 if (current.nodeName === 'SELECT' && current.value !== next.value) current.value = next.value;
 if (current.nodeName === 'INPUT' && ['checkbox','radio'].includes(current.type) && current.checked !== next.checked) current.checked = next.checked;
}

function patchChildren(current, next) {
 const previous = [...current.childNodes], keyed = new Map(previous.filter(nodeKey).map(n => [nodeKey(n), n]));
 const used = new Set();
 let cursor = current.firstChild;
 for (const child of [...next.childNodes]) {
  const key = nodeKey(child);
  let match = key ? keyed.get(key) : previous.find(n => !used.has(n) && !nodeKey(n) && compatible(n, child));
  if (match && !compatible(match, child)) match = null;
  if (!match) match = child.cloneNode(true);
  if (match !== cursor) current.insertBefore(match, cursor);
  used.add(match);
  patchNode(match, child);
  cursor = match.nextSibling;
 }
 for (const old of previous) if (!used.has(old)) old.remove();
}

export function patchHTML(element, html) {
 const active = element.ownerDocument.activeElement;
 const hadFocus = element.contains(active);
 const ownerKey = hadFocus ? active.closest?.('[data-dom-key]')?.getAttribute('data-dom-key') : null;
 const draft = element.ownerDocument.createElement('template');
 draft.innerHTML = html;
 patchChildren(element, draft.content);
 if (hadFocus && !active.isConnected) {
  const owner = ownerKey ? [...element.querySelectorAll('[data-dom-key]')].find(n => n.getAttribute('data-dom-key') === ownerKey) : element;
  owner?.querySelector('button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),summary')?.focus({preventScroll:true});
 }
}

// Stream in new results without moving a resource that the user is reading.
// Changing a filter/sort or choosing "更新排序" resets the order explicitly.
export function stableResultOrder(ranked, previous = []) {
 const available = new Map(ranked.map(row => [row.id, row]));
 const ids = [...previous.filter(id => available.has(id)), ...ranked.filter(row => !previous.includes(row.id)).map(row => row.id)];
 return { rows: ids.map(id => available.get(id)), ids, changed: ids.some((id, i) => id !== ranked[i]?.id) };
}
