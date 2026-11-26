// Tiny element builder. It only ever sets text with textContent and attributes
// with setAttribute, never HTML, so nothing the API returns can inject markup.
const SVG = 'http://www.w3.org/2000/svg';

function build(el, props, kids) {
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.setAttribute('class', v);
    else if (k === 'text') el.textContent = v;
    else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : String(v));
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

export const h = (tag, props, ...kids) => build(document.createElement(tag), props, kids);
export const s = (tag, props, ...kids) => build(document.createElementNS(SVG, tag), props, kids);
export const clear = (el) => { el.replaceChildren(); return el; };
