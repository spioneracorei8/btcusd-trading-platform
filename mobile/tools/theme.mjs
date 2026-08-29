/**
 * Reads the design tokens out of src/theme/colors.ts.
 *
 * # Why the tools read them rather than repeating them
 *
 * The lint rule that keeps colour literals inside src/theme/ covers the app's
 * source. The tools in this directory sit outside it and used to carry their
 * own copies, which is the drift this whole convention exists to prevent — and
 * here it was worse than cosmetic. tools/audit.mjs enforces the gold-area cap
 * by matching painted colours against a list of gold tokens: change a gold
 * token without updating that list and the audit stops recognising gold at
 * all, so the cap passes on everything and reports success.
 *
 * A regex over TypeScript is coarse. It is also the only thing available to a
 * Node script that cannot import the file, and src/theme/theme.test.ts checks
 * that what comes out of here matches what the app imports.
 */
import { readFileSync } from 'node:fs';

const SOURCE = 'src/theme/colors.ts';

/** Every `<key>: '#rrggbb'` inside `export const <group> = { ... } as const;`. */
export function group(name, source = readFileSync(SOURCE, 'utf8')) {
  const block = new RegExp(`export const ${name} = \\{([\\s\\S]*?)\\n\\} as const;`).exec(source);
  if (!block) throw new Error(`no token group "${name}" in ${SOURCE}`);

  const values = {};
  for (const [, key, hex] of block[1].matchAll(/\b(\w+):\s*'(#[0-9a-fA-F]{3,8})'/g)) {
    values[key] = hex;
  }
  if (Object.keys(values).length === 0) throw new Error(`no colours in "${name}"`);
  return values;
}

/** One token, by group and key. */
export function token(name, key, source = readFileSync(SOURCE, 'utf8')) {
  const value = group(name, source)[key];
  if (!value) throw new Error(`no token "${name}.${key}" in ${SOURCE}`);
  return value;
}

/**
 * Relative luminance, the WCAG definition.
 *
 * Shared because two tools ask the same question of it: the audit, for "is
 * anything brighter than text.primary", and this module, for computing that
 * ceiling rather than repeating it as a number.
 */
export function luminance(hex) {
  const channel = (value) => {
    const c = value / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  const [r, g, b] = [1, 3, 5].map((at) => parseInt(hex.slice(at, at + 2), 16));
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}
