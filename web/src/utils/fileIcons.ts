// fileIcons.ts — emoji icon per file-list entry, picked from the entry type
// and file name. Unknown files fall back to the generic page icon.

// Well-known file names checked before the extension map.
const NAME_ICONS: Record<string, string> = {
  'cmakelists.txt': '🛠️',
  justfile: '🛠️',
  gnumakefile: '🛠️',
  'pom.xml': '📦',
  'build.gradle': '🛠️',
  'settings.gradle': '🛠️',
  'package.json': '📦',
  'composer.json': '🐘',
  gemfile: '💎',
  'go.mod': '📦',
  'go.sum': '📦',
  'cargo.toml': '📦',
  'package-lock.json': '🔒',
  'pnpm-lock.yaml': '🔒',
  'yarn.lock': '🔒',
  'composer.lock': '🔒',
  'poetry.lock': '🔒',
  'cargo.lock': '🔒',
  'gemfile.lock': '🔒',
}

// Extension → icon. Dotfiles work too: for ".gitignore" the "extension" is
// the whole name minus the leading dot.
const EXT_ICONS: Record<string, string> = {
  // images
  png: '🖼️', apng: '🖼️', jpg: '🖼️', jpeg: '🖼️', jfif: '🖼️', gif: '🖼️',
  webp: '🖼️', avif: '🖼️', bmp: '🖼️', ico: '🖼️', svg: '🖼️', tif: '🖼️',
  tiff: '🖼️', heic: '🖼️', heif: '🖼️',
  // audio
  mp3: '🎵', wav: '🎵', ogg: '🎵', flac: '🎵', m4a: '🎵', aac: '🎵',
  opus: '🎵', wma: '🎵', mid: '🎵', midi: '🎵',
  // video
  mp4: '🎬', m4v: '🎬', mkv: '🎬', webm: '🎬', mov: '🎬', avi: '🎬',
  wmv: '🎬', flv: '🎬', mpg: '🎬', mpeg: '🎬', '3gp': '🎬',
  // archives, packages, disk images
  zip: '📦', tar: '📦', tgz: '📦', gz: '📦', bz2: '📦', xz: '📦',
  zst: '📦', '7z': '📦', rar: '📦', jar: '📦', war: '📦', lz4: '📦',
  br: '📦', deb: '📦', rpm: '📦', apk: '📦', ipynb: '📓',
  iso: '💿', img: '💿', dmg: '💿',
  // documents
  pdf: '📕',
  doc: '📘', docx: '📘', odt: '📘', rtf: '📘', pages: '📘',
  xls: '📗', xlsx: '📗', ods: '📗', csv: '📗', tsv: '📗', numbers: '📗',
  ppt: '📙', pptx: '📙', odp: '📙',
  txt: '📝', text: '📝', md: '📝', markdown: '📝', rst: '📝',
  adoc: '📝', org: '📝', tex: '📝', log: '📝',
  // databases & structured data
  sql: '🗄️', db: '🗄️', sqlite: '🗄️', sqlite3: '🗄️', mdb: '🗄️',
  json: '🗃️', jsonc: '🗃️', json5: '🗃️', ndjson: '🗃️', xml: '🗃️',
  gql: '🗃️', graphql: '🗃️', proto: '🗃️',
  // config
  yaml: '🔧', yml: '🔧', toml: '🔧', ini: '🔧', cfg: '🔧', conf: '🔧',
  config: '🔧', properties: '🔧', env: '🔧', plist: '🔧',
  gitignore: '🔧', gitattributes: '🔧', gitmodules: '🔧', gitconfig: '🔧',
  editorconfig: '🔧', dockerignore: '🔧', npmrc: '🔧', nvmrc: '🔧',
  eslintrc: '🔧', prettierrc: '🔧', babelrc: '🔧', vimrc: '🔧',
  // keys & certificates
  pem: '🔑', key: '🔑', crt: '🔑', cer: '🔑', der: '🔑', p12: '🔑',
  pfx: '🔑', pub: '🔑', asc: '🔑', gpg: '🔑', lock: '🔒',
  // fonts
  ttf: '🔤', otf: '🔤', woff: '🔤', woff2: '🔤', eot: '🔤',
  // executables & binaries
  exe: '⚙️', bin: '⚙️', dll: '⚙️', so: '⚙️', dylib: '⚙️', a: '⚙️',
  o: '⚙️', obj: '⚙️', lib: '⚙️', class: '⚙️', wasm: '⚙️', pyc: '⚙️',
  out: '⚙️', com: '⚙️', msi: '⚙️',
  // shells
  sh: '🐚', bash: '🐚', zsh: '🐚', fish: '🐚', ksh: '🐚', nu: '🐚',
  ps1: '🐚', psm1: '🐚', bat: '🐚', cmd: '🐚',
  bashrc: '🐚', zshrc: '🐚', profile: '🐚', bash_profile: '🐚',
  // code — languages with a recognizable mascot get theirs
  go: '🐹', rs: '🦀', py: '🐍', pyw: '🐍', pyi: '🐍', rb: '💎',
  java: '☕', php: '🐘', dart: '🎯', lua: '🌙', swift: '🐦',
  js: '🟨', mjs: '🟨', cjs: '🟨', jsx: '🟨', coffee: '🟨',
  ts: '🟦', tsx: '🟦', mts: '🟦', cts: '🟦',
  vue: '🟢', svelte: '🟠',
  html: '🌐', htm: '🌐', xhtml: '🌐',
  css: '🎨', scss: '🎨', sass: '🎨', less: '🎨',
  // other source files
  c: '📜', h: '📜', cpp: '📜', cc: '📜', cxx: '📜', hpp: '📜', hxx: '📜',
  cs: '📜', fs: '📜', fsx: '📜', clj: '📜', cljs: '📜', ex: '📜',
  exs: '📜', erl: '📜', hrl: '📜', elm: '📜', hs: '📜', lhs: '📜',
  ml: '📜', mli: '📜', r: '📜', jl: '📜', pl: '📜', pm: '📜', tcl: '📜',
  vim: '📜', sol: '📜', zig: '📜', nim: '📜', v: '📜', d: '📜',
  f90: '📜', f95: '📜', pas: '📜', asm: '📜', s: '📜', scala: '📜',
  kt: '📜', kts: '📜', groovy: '📜', diff: '📜', patch: '📜',
}

/** Icon for a file-list entry. `path` may be a bare name or a full path. */
export function fileIcon(path: string, type = 'file'): string {
  if (type === 'dir') return '📁'
  if (type === 'link') return '🔗'
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase()

  if (name.startsWith('dockerfile') || name.endsWith('.dockerfile')) return '🐳'
  if (name === 'makefile' || name.startsWith('makefile.')) return '🛠️'
  if (name === 'readme' || name.startsWith('readme.')) return '📖'
  if (/^(licen[sc]e|copying)(\.|$)/.test(name)) return '⚖️'
  if (name === '.env' || name.startsWith('.env.')) return '🔧'

  if (Object.hasOwn(NAME_ICONS, name)) return NAME_ICONS[name]

  const ext = name.includes('.') ? name.slice(name.lastIndexOf('.') + 1) : ''
  return Object.hasOwn(EXT_ICONS, ext) ? EXT_ICONS[ext] : '📄'
}
