// A short, readable name for what a workspace runs ("Go 1.27", "Android", "walroos base"),
// instead of its full image reference. The full image stays in the tooltip.

const names: Record<string, string> = {
  android: 'Android',
  dotnet: '.NET',
  bun: 'Bun',
  node: 'Node.js',
  go: 'Go',
  rust: 'Rust',
  python: 'Python',
  java: 'Java',
};

// The devcontainers image name for each stack name above, to recognise images without a stored stack.
const imageStacks: Record<string, string> = {
  go: 'go',
  dotnet: 'dotnet',
  'javascript-node': 'node',
  'typescript-node': 'node',
  python: 'python',
  rust: 'rust',
  java: 'java',
};

// ref splits "registry/path/name:tag" (or "@sha256:…") into its last path segment and tag.
function ref(image: string): { name: string; tag: string } {
  const at = image.indexOf('@');
  const noDigest = at >= 0 ? image.slice(0, at) : image;
  const slash = noDigest.lastIndexOf('/');
  const colon = noDigest.lastIndexOf(':');
  const tag = colon > slash ? noDigest.slice(colon + 1) : '';
  const path = colon > slash ? noDigest.slice(0, colon) : noDigest;
  return { name: path.slice(path.lastIndexOf('/') + 1), tag };
}

// version picks the language version out of a devcontainers tag: "1.27" stays, "1-17-bookworm"
// (image version - language version - distro) gives "17". Anything else gives none.
function version(tag: string): string {
  if (/^\d+(\.\d+)*$/.test(tag)) return tag;
  const m = /^\d+-(\d+(?:\.\d+)*)-[a-z]+$/.exec(tag);
  return m ? m[1] : '';
}

export function stackLabel(stack: string | undefined, image: string | undefined): string {
  const img = image ?? '';
  const { name, tag } = ref(img);
  const devcontainers = img.includes('/devcontainers/');

  if (stack && names[stack]) {
    // Android runs on a java image: its tag is the JDK, not the Android version.
    const v = stack !== 'android' && devcontainers ? version(tag) : '';
    return v ? `${names[stack]} ${v}` : names[stack];
  }
  if (!img) return '';
  if (img.startsWith('localhost/walroos-base:')) return 'walroos base';
  if (img.startsWith('localhost/walroos-ws-')) return 'Dockerfile';
  if (devcontainers && imageStacks[name]) {
    const v = version(tag);
    const label = names[imageStacks[name]];
    return v ? `${label} ${v}` : label;
  }
  return tag ? `${name}:${tag}` : name;
}
