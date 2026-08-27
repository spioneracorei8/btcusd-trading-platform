import fs from 'fs';
import path from 'path';

import { colors, direction, gold, budget } from './colors';
import { size, tabular } from './type';
import { motion, space, radius } from './layout';

/**
 * TestNoColourLiteralOutsideTheTheme, in the form a test can check.
 *
 * The lint rule is the enforcement; this is the same rule expressed where a
 * failure names the file, because a lint config can be disabled in a moment
 * and a failing test is louder.
 */
describe('the palette is defined in one place', () => {
  const srcDir = path.join(__dirname, '..');

  /** Every .ts/.tsx under src/, except the theme itself. */
  function sourceFiles(dir: string): string[] {
    return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        return entry.name === 'theme' ? [] : sourceFiles(full);
      }
      return /\.tsx?$/.test(entry.name) ? [full] : [];
    });
  }

  it('has no hex colour outside src/theme', () => {
    const offenders = sourceFiles(srcDir)
      .map((file) => ({ file, body: fs.readFileSync(file, 'utf8') }))
      .filter(({ body }) => /#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})\b/.test(body))
      .map(({ file }) => path.relative(srcDir, file));

    expect(offenders).toEqual([]);
  });

  it('has no rgb() or hsl() outside src/theme', () => {
    const offenders = sourceFiles(srcDir)
      .map((file) => ({ file, body: fs.readFileSync(file, 'utf8') }))
      .filter(({ body }) => /\b(?:rgba?|hsla?)\s*\(/.test(body))
      .map(({ file }) => path.relative(srcDir, file));

    expect(offenders).toEqual([]);
  });
});

/**
 * The rules from phase-09 §E that are checkable as values rather than as
 * rendered pixels. The rendered half is the screenshot audit.
 */
describe('the tokens hold the rules they were chosen for', () => {
  it('never uses pure white for text', () => {
    // Pure white on dark is the commonest cause of eye strain in night use,
    // and this is a screen read at night by definition.
    for (const [name, value] of Object.entries(colors.text)) {
      expect(value.toLowerCase()).not.toBe('#ffffff');
      expect(value.toLowerCase()).not.toBe('#fff');
      expect(name).toBeTruthy();
    }
  });

  it('never uses pure black for a surface', () => {
    // Pure black makes gold vibrate against it.
    for (const value of Object.values(colors.bg)) {
      expect(value.toLowerCase()).not.toBe('#000000');
      expect(value.toLowerCase()).not.toBe('#000');
    }
  });

  it('gives long the interface green and short something else entirely', () => {
    // A long in a green that is not the UI green would read as chrome. The
    // point of reusing jade.base is that a long IS the ordinary state.
    expect(direction.long).toBe(colors.jade.base);
    expect(direction.short).not.toBe(direction.long);
  });

  it('keeps semantic state distinct from direction', () => {
    // A losing trade must not look like a system fault, so `error` is not
    // `short` however tempting the shared warmth is.
    expect(colors.semantic.error).not.toBe(direction.short);
    expect(colors.semantic.error).not.toBe(direction.long);
  });

  it('sets sample sizes at 14, not caption size', () => {
    // The single most important typographic rule in the app. A sample size is
    // a qualifier on the number beside it, and a qualifier in caption size
    // loses to the figure it qualifies.
    expect(size.sampleSize).toBe(14);
    expect(size.sampleSize).toBeGreaterThan(size.caption);
  });

  it('offers only the five sizes on the scale', () => {
    const scale = new Set([28, 20, 16, 14, 12]);
    for (const value of Object.values(size)) {
      expect(scale.has(value)).toBe(true);
    }
  });

  it('asks for tabular figures', () => {
    // Proportional digits make a live price jitter horizontally as it updates.
    expect(tabular.fontVariant).toContain('tabular-nums');
  });

  it('does not animate a number changing', () => {
    // A price sliding between two values is unreadable while it moves, and
    // implies a continuum the data does not have.
    expect(motion.numbers).toBe(0);
  });

  it('keeps transitions short and never springy', () => {
    expect(motion.fade).toBeLessThanOrEqual(200);
    expect(motion.transition).toBeLessThanOrEqual(250);
    expect(motion.easing).toBe('ease-out');
  });

  it('uses one corner radius, and a small one', () => {
    // An instrument, not a lifestyle app.
    expect(radius).toBe(8);
  });

  it('puts every spacing value on the 4pt scale', () => {
    for (const value of Object.values(space)) {
      expect(value % 4).toBe(0);
    }
  });

  it('budgets gold as an accent rather than a fill', () => {
    // Anything larger than roughly a 24pt square is a fill, and a fill is
    // what turns an instrument into a celebration.
    expect(budget.maxGoldAreaPt).toBeLessThanOrEqual(24 * 24);
    expect(Object.keys(gold)).toEqual(['dim', 'base', 'bright']);
  });
});
