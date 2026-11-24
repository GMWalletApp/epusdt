import{r as e}from"./chunk-DECur_0Z.js";import{a as t}from"./auth-store-A8BVO9Va.js";import{t as n}from"./react-CO2uhaBc.js";import{Hn as r,Og as i,kn as a}from"./messages-x8gi0-C9.js";import{n as o}from"./Match-CFa023d7.js";import{i as s}from"./button-DjNfY7iY.js";import{t as c}from"./cookies-sGpLNHu8.js";import{t as l}from"./createLucideIcon-CWEVq4aQ.js";import{n as u,t as d}from"./theme-switch-D61ggBTc.js";import{h as f}from"./checkout-model--Uaj3eqg.js";var p=l(`message-circle`,[[`path`,{d:`M2.992 16.342a2 2 0 0 1 .094 1.167l-1.065 3.29a1 1 0 0 0 1.236 1.168l3.413-.998a2 2 0 0 1 1.099.092 10 10 0 1 0-4.777-4.719`,key:`1sd12s`}]]),m=e(n(),1),h=i();function g({className:e,...t}){return(0,h.jsxs)(`svg`,{className:s(`[&>path]:stroke-current`,e),fill:`none`,height:`24`,role:`img`,stroke:`currentColor`,strokeLinecap:`round`,strokeLinejoin:`round`,strokeWidth:`2`,viewBox:`0 0 24 24`,width:`24`,xmlns:`http://www.w3.org/2000/svg`,...t,children:[(0,h.jsx)(`title`,{children:`GitHub`}),(0,h.jsx)(`path`,{d:`M0 0h24v24H0z`,fill:`none`,strokeWidth:`0`}),(0,h.jsx)(`path`,{d:`M9 19c-4.3 1.4 -4.3 -2.5 -6 -3m12 5v-3.5c0 -1 .1 -1.4 -.5 -2c2.8 -.3 5.5 -1.4 5.5 -6a4.6 4.6 0 0 0 -1.3 -3.2a4.2 4.2 0 0 0 -.1 -3.2s-1.1 -.3 -3.5 1.3a12.3 12.3 0 0 0 -6.2 0c-2.4 -1.6 -3.5 -1.3 -3.5 -1.3a4.2 4.2 0 0 0 -.1 3.2a4.6 4.6 0 0 0 -1.3 3.2c0 4.6 2.7 5.7 5.5 6c-.6 .6 -.6 1.2 -.5 2v3.5`})]})}var _=`// This bridge runs inside the opaque widget frame. It reports only the visible
// widget bounds so the surrounding checkout remains clickable.
(() => {
	// Opaque frames have no cookie jar. Keep SDK preferences in this document
	// only. Nothing here reads or writes checkout or admin cookies.
	const cookies = new Map();
	Object.defineProperty(document, "cookie", {
		get: () =>
			Array.from(cookies, ([key, value]) => \`\${key}=\${value}\`).join("; "),
		set(line) {
			const [pair, ...attrs] = String(line).split(";");
			const at = pair.indexOf("=");
			if (at < 1) {
				return;
			}
			const key = pair.slice(0, at).trim();
			const expired = attrs.some(
				(attr) =>
					/^\\s*max-age\\s*=\\s*(?:0|-\\d+)\\s*$/i.test(attr) ||
					(/^\\s*expires=/i.test(attr) &&
						Date.parse(attr.split("=").slice(1).join("=")) <= Date.now())
			);
			if (expired) {
				cookies.delete(key);
			} else {
				cookies.set(key, pair.slice(at + 1));
			}
		},
	});
	// Chatwoot creates a provider-hosted chat URL before its own launcher
	// is ready. Expose that link if the nested app cannot use browser storage.
	let offered;
	let chatReady = false;
	addEventListener("chatwoot:ready", () => {
		chatReady = true;
		schedule();
	});

	let scheduled = false;
	let previous = "";
	const observed = new Set();
	const resize = new ResizeObserver(schedule);

	function schedule() {
		if (!scheduled) {
			scheduled = true;
			// A fully clipped cross-origin frame can have animation frames suspended.
			setTimeout(measure, 16);
		}
	}

	function measure() {
		scheduled = false;
		const provider = document.querySelector("iframe#chatwoot_live_chat_widget");
		const chat = !chatReady && provider ? provider.src : "";
		if (chat !== offered) {
			offered = chat;
			parent.postMessage({ type: "epusdt-chatwoot-link", url: chat }, "*");
		}
		const visible = new Set();
		let left = innerWidth;
		let top = innerHeight;
		let right = 0;
		let bottom = 0;
		for (const node of document.body.querySelectorAll("*")) {
			const box = node.getBoundingClientRect();
			if (!(box.width && box.height)) {
				continue;
			}
			const style = getComputedStyle(node);
			if (style.visibility === "hidden" || style.opacity === "0") {
				continue;
			}
			visible.add(node);
			if (!observed.has(node)) {
				resize.observe(node);
			}
			// Transparent layout wrappers can cover the entire frame without
			// painting anything or accepting input.
			if (
				node.children.length &&
				!node.shadowRoot &&
				style.backgroundColor === "rgba(0, 0, 0, 0)" &&
				style.backgroundImage === "none" &&
				!node.matches("button,a,svg,iframe,[role=button]")
			) {
				continue;
			}
			left = Math.min(left, box.left);
			top = Math.min(top, box.top);
			right = Math.max(right, box.right);
			bottom = Math.max(bottom, box.bottom);
		}
		for (const node of observed) {
			if (!visible.has(node)) {
				resize.unobserve(node);
			}
		}
		observed.clear();
		for (const node of visible) {
			observed.add(node);
		}
		const bounds = [top, innerWidth - right, innerHeight - bottom, left].map(
			(px) => Math.max(0, Math.floor(px))
		);
		const key = bounds.join(",");
		if (key !== previous) {
			previous = key;
			parent.postMessage({ type: "epusdt-widget-bounds", bounds }, "*");
		}
	}
	new MutationObserver(schedule).observe(document.body, {
		attributes: true,
		childList: true,
		subtree: true,
		characterData: true,
	});
	for (const event of ["load", "resize", "transitionend", "animationend"]) {
		addEventListener(event, schedule, true);
	}
	schedule();
})();
`,v=new WeakSet;async function y(e){if(v.has(document))return;v.add(document);let t=document.createElement(`template`);t.innerHTML=e;for(let e of t.content.querySelectorAll(`script`)){let t=document.createElement(`script`);for(let n of e.attributes)t.setAttribute(n.name,n.value);t.textContent=e.textContent;let n=t.hasAttribute(`src`)&&!t.hasAttribute(`async`);n&&(t.async=!1);let r=n&&new Promise(e=>{t.addEventListener(`load`,()=>e(),{once:!0}),t.addEventListener(`error`,()=>e(),{once:!0})});document.body.appendChild(t),r&&await r}}function b({snippet:e,nativeOrigin:t}){let[n,i]=(0,m.useState)(``),a=(0,m.useRef)(null),o=location.protocol===`https:`&&location.origin===t&&!c(`access_token`);return(0,m.useEffect)(()=>{if(o){let t=!0;return queueMicrotask(()=>{t&&y(e)}),()=>{t=!1}}function t(e){let t=a.current;if(!t||e.source!==t.contentWindow||e.origin!==`null`)return;if(e.data?.type===`epusdt-chatwoot-link`){let t=typeof e.data.url==`string`?URL.parse(e.data.url):null,n=t?.searchParams.get(`website_token`);t&&n&&[`https:`,`http:`].includes(t.protocol)&&t.pathname.endsWith(`/widget`)&&!t.username&&!t.password?(t.search=new URLSearchParams({website_token:n}).toString(),t.hash=``,i(t.href)):i(``);return}let n=e.data?.bounds;e.data?.type!==`epusdt-widget-bounds`||!Array.isArray(n)||n.length!==4||!n.every(e=>Number.isFinite(e)&&e>=0)||(t.style.clipPath=`inset(${n.map(e=>`${e}px`).join(` `)})`)}return window.addEventListener(`message`,t),()=>window.removeEventListener(`message`,t)},[o,e]),o?null:(0,h.jsxs)(h.Fragment,{children:[(0,h.jsx)(`iframe`,{className:`fixed right-0 bottom-0 z-20 h-[min(100dvh,42rem)] w-[min(100vw,27rem)] border-0`,ref:a,referrerPolicy:`no-referrer`,sandbox:`allow-scripts allow-forms allow-popups`,srcDoc:`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><style>body{margin:0}</style></head><body><script>${_}<\/script>${e}</body></html>`,style:{clipPath:`inset(100%)`},title:r()}),n&&(0,h.jsx)(`a`,{className:`fixed right-5 bottom-5 z-30 flex h-12 items-center rounded-full bg-primary px-4 font-medium text-primary-foreground text-sm shadow-lg`,href:n,rel:`noopener noreferrer`,target:`_blank`,children:`Chatwoot`})]})}function x(){let e=t.useQuery(`get`,`/payments/gmpay/v1/config`,void 0,{retry:!1}),n=(0,m.useMemo)(()=>f(e.data),[e.data])?.site,i=n?.cashier_name||`GM Pay`,c=n?.logo_url||`/images/logo.png`,l=n?.website_title||i,_=n?.support_link,v=n?.cashier_script,y=n?.background_color?.trim(),x=n?.background_image_url?.trim(),S=(0,m.useMemo)(()=>({...y&&!x?{backgroundColor:y}:{},...x&&y?{backgroundAttachment:`fixed`,backgroundImage:`linear-gradient(${y}, ${y}), url(${JSON.stringify(x)})`,backgroundPosition:`center, center`,backgroundRepeat:`no-repeat, no-repeat`,backgroundSize:`cover, cover`}:{},...x&&!y?{backgroundAttachment:`fixed`,backgroundImage:`url(${JSON.stringify(x)})`,backgroundPosition:`center`,backgroundRepeat:`no-repeat`,backgroundSize:`cover`}:{}}),[y,x]);return(0,m.useEffect)(()=>{document.title=l},[l]),(0,h.jsx)(`div`,{className:`min-h-svh bg-background text-foreground`,style:S,children:(0,h.jsxs)(`div`,{className:`mx-auto flex min-h-svh w-full max-w-sm flex-col px-5`,children:[(0,h.jsxs)(`header`,{className:`flex items-center justify-between pt-8 pb-6`,children:[(0,h.jsxs)(`div`,{className:`flex items-center gap-2`,children:[(0,h.jsx)(`img`,{alt:i,className:`size-8 rounded-sm shadow-sm`,height:32,src:c,width:32}),(0,h.jsx)(`span`,{className:`font-semibold text-card-foreground text-lg tracking-tight`,children:i})]}),(0,h.jsxs)(`div`,{className:`flex items-center gap-2`,children:[(0,h.jsx)(u,{}),(0,h.jsx)(d,{})]})]}),(0,h.jsx)(o,{}),v?(0,h.jsx)(b,{nativeOrigin:n?.cashier_script_origin,snippet:v},v):null,_?(0,h.jsxs)(`a`,{"aria-label":r(),className:s(`fixed bottom-5 z-20 flex h-12 items-center gap-2 rounded-full bg-primary px-4 font-medium text-primary-foreground text-sm shadow-lg transition hover:opacity-90`,v?`left-5`:`right-5`),href:_,rel:`noopener noreferrer`,target:`_blank`,children:[(0,h.jsx)(p,{className:`size-4`}),(0,h.jsx)(`span`,{children:r()})]}):null,(0,h.jsxs)(`footer`,{className:`flex flex-wrap items-center justify-center gap-2.5 py-6 text-muted-foreground text-xs`,children:[(0,h.jsxs)(`span`,{className:`flex items-center gap-1.5`,children:[a(),(0,h.jsxs)(`a`,{className:`flex items-center gap-1 font-semibold text-card-foreground hover:opacity-70`,href:`https://www.gmwallet.app`,rel:`noopener noreferrer`,target:`_blank`,children:[(0,h.jsx)(`img`,{alt:``,className:`size-3.5 rounded-xs`,height:14,src:`/images/logo.png`,width:14}),`GM Wallet`]})]}),(0,h.jsx)(`span`,{className:`opacity-30`,children:`|`}),(0,h.jsxs)(`span`,{className:`flex items-center gap-1.5`,children:[`Open source on`,(0,h.jsxs)(`a`,{className:`flex items-center gap-1 font-semibold text-card-foreground hover:opacity-70`,href:`https://github.com/GMwalletApp`,rel:`noopener noreferrer`,target:`_blank`,children:[(0,h.jsx)(g,{className:`size-3.5`}),`GitHub`]})]})]})]})})}export{x as component};