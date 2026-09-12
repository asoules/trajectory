// Reconcile rendered HTML without remounting panels, inputs, or scroll containers.
// Keys make span rows stable when new operations arrive or groups change rank.
export function patchHTML(target, html) {
  const template = document.createElement("template");
  template.innerHTML = html;
  const focus = document.activeElement;
  patchChildren(target, template.content);
  if (focus?.isConnected && document.activeElement !== focus)
    focus.focus({ preventScroll: true });
}
const nodeKey = (node) =>
  node.nodeType === 1 ? node.id || node.getAttribute("data-key") : null;
function patchChildren(target, source) {
  const keyed = new Map(
    [...target.childNodes].map((n) => [nodeKey(n), n]).filter(([key]) => key),
  );
  let current = target.firstChild;
  for (const next of [...source.childNodes]) {
    const key = nodeKey(next);
    let match = key ? keyed.get(key) : !nodeKey(current ?? {}) ? current : null;
    if (key) keyed.delete(key); // Never reuse one element for duplicate incoming keys.
    if (
      match &&
      (match.nodeType !== next.nodeType || match.nodeName !== next.nodeName)
    )
      match = null;
    if (!match) {
      match = next.cloneNode(true);
      target.insertBefore(match, current);
    } else {
      if (match !== current) target.insertBefore(match, current);
      if (match.nodeType === 3) {
        if (match.nodeValue !== next.nodeValue)
          match.nodeValue = next.nodeValue;
      } else if (match.nodeType === 1) {
        // Do not disturb an open native select or an edit in progress.
        if (!(
          match === document.activeElement &&
          match.matches("input,select,textarea")
        )) {
          for (const attr of [...match.attributes])
            if (
              !(match.tagName === "DETAILS" && attr.name === "open") &&
              !next.hasAttribute(attr.name)
            )
              match.removeAttribute(attr.name);
          for (const attr of [...next.attributes])
            if (
              !(match.tagName === "DETAILS" && attr.name === "open") &&
              match.getAttribute(attr.name) !== attr.value
            )
              match.setAttribute(attr.name, attr.value);
          if (!next.hasAttribute("data-preserve")) patchChildren(match, next);
          if (match.tagName === "SELECT" && match.value !== next.value)
            match.value = next.value;
        }
      }
    }
    current = match.nextSibling;
  }
  while (current) {
    const next = current.nextSibling;
    target.removeChild(current);
    current = next;
  }
}
