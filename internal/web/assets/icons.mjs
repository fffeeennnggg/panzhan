// Functional interface icons, drawn on one consistent 24px grid.
const paths={
 search:'<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/>',
 tasks:'<path d="M12 3v12m-4-4 4 4 4-4M4 16v4a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-4"/>',
 account:'<path d="M7 18H6a4 4 0 0 1-.7-7.94A7 7 0 0 1 19 8.5a4.75 4.75 0 0 1-.25 9.5H17"/><circle cx="12" cy="14" r="2"/><path d="M8 21a4 4 0 0 1 8 0"/>',
 sources:'<path d="m12 3 9 5-9 5-9-5 9-5Zm-9 9 9 5 9-5M3 16l9 5 9-5"/>',
 settings:'<path d="M4 7h9m4 0h3M4 17h3m4 0h9"/><circle cx="15" cy="7" r="2"/><circle cx="9" cy="17" r="2"/>',
 arrow:'<path d="M5 12h14m-5-5 5 5-5 5"/>',
 plus:'<path d="M12 5v14M5 12h14"/>',
 link:'<path d="m10 14 4-4M8 16l-2 2a3 3 0 0 1-4-4l4-4a3 3 0 0 1 4 0m4-2 2-2a3 3 0 0 1 4 4l-4 4a3 3 0 0 1-4 0"/>',
 logout:'<path d="M9 4H5a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h4m5-12 4 4-4 4m-6-4h10"/>',
 folder:'<path d="M3 7V5h6l2 2h10v13H3V7Z"/>',
 check:'<path d="m5 12 4 4L19 6"/>',
 shield:'<path d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6l8-3Z"/><path d="m8 12 3 3 5-6"/>',
 video:'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m10 8 6 4-6 4V8Z"/>',
 audio:'<path d="M9 18V5l11-2v13M9 8l11-2"/><ellipse cx="6" cy="18" rx="3" ry="2"/><ellipse cx="17" cy="16" rx="3" ry="2"/>',
 document:'<path d="M14 3H5v18h14V8l-5-5Zm0 0v5h5M8 12h8m-8 4h6"/>',
 archive:'<rect x="4" y="3" width="16" height="18" rx="2"/><path d="M12 3v11m-2 0h4v4h-4v-4Z"/>',
 image:'<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8" cy="8" r="1"/><path d="m3 17 5-5 4 4 4-7 5 8"/>',
 file:'<path d="M14 3H5v18h14V8l-5-5Zm0 0v5h5"/>',
 lock:'<rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3m-4 5v2"/>',
 close:'<path d="m6 6 12 12M6 18 18 6"/>',
 pause:'<path d="M8 5v14M16 5v14"/>',
 play:'<path d="m8 4 12 8-12 8V4Z"/>'
};
export function icon(name){return `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name]||paths.file}</svg>`;}
