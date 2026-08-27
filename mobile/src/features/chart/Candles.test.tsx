import { render, screen } from '@testing-library/react-native';
import { Rect } from 'react-native-svg';

import { Candles, markersFor } from './Candles';
import { colors } from '../../theme';
import type { Candle, Outcome, Signal } from '../../api/types';

function bar(overrides: Partial<Candle> = {}): Candle {
  return {
    open_time: '2026-08-27T08:00:00Z',
    close_time: '2026-08-27T11:59:59.999Z',
    open: '64000',
    high: '64500',
    low: '63800',
    close: '64200',
    volume: '1.5',
    is_closed: true,
    ...overrides,
  };
}

/** The candle bodies, in the order they were drawn. */
function bodies() {
  return screen.UNSAFE_getAllByType(Rect).filter((node) => {
    const { width, height } = node.props as { width?: number; height?: number };
    // Markers are 6x6 squares; bodies are as wide as their slot.
    return !(width === 6 && height === 6);
  });
}

/**
 * TestAFormingCandleIsDrawnAsOne.
 *
 * # What this prevents
 *
 * The websocket is the one place in this system permitted to send a bar that
 * has not closed, and the flag on the wire is what makes that safe. A flag
 * nothing renders is a flag nobody sees: the chart would draw a price that can
 * still change identically to one that cannot, and a person reading the chart
 * would have no way to know which they were looking at.
 *
 * So the flag has to survive from the wire into something the eye can
 * distinguish. Hollow rather than filled, because that reads as provisional
 * without needing a legend.
 */
describe('a forming candle', () => {
  it('is drawn hollow while a closed one is filled', () => {
    render(
      <Candles
        candles={[
          bar({ open_time: '2026-08-27T08:00:00Z', is_closed: true }),
          bar({ open_time: '2026-08-27T12:00:00Z', is_closed: false }),
        ]}
        width={200}
        height={100}
      />,
    );

    const [closed, forming] = bodies();

    expect(closed!.props.fill).toBe(colors.direction.long);
    expect(forming!.props.fill).toBe('none');
  });

  it('is drawn at a lower opacity than a closed one', () => {
    render(
      <Candles
        candles={[
          bar({ open_time: '2026-08-27T08:00:00Z', is_closed: true }),
          bar({ open_time: '2026-08-27T12:00:00Z', is_closed: false }),
        ]}
        width={200}
        height={100}
      />,
    );

    const [closed, forming] = bodies();

    expect(Number(forming!.props.opacity)).toBeLessThan(Number(closed!.props.opacity));
  });

  it('is still drawn — the chart is where a forming bar is legitimate', () => {
    // The rule is that it must be flagged, not that it must be hidden. A chart
    // that dropped the newest bar would be a chart that is always a bar stale.
    render(<Candles candles={[bar({ is_closed: false })]} width={200} height={100} />);

    expect(bodies()).toHaveLength(1);
  });
});

/**
 * The chart uses the direction colours and nothing else. A gold gridline would
 * be the largest gold area in the app by an order of magnitude, and it would
 * sit behind the one part of the screen meant to be read rather than admired.
 */
describe('the chart palette', () => {
  it('draws rising and falling bars in the direction colours', () => {
    render(
      <Candles
        candles={[
          bar({ open_time: '2026-08-27T00:00:00Z', open: '64000', close: '64500' }),
          bar({ open_time: '2026-08-27T04:00:00Z', open: '64500', close: '64000' }),
        ]}
        width={200}
        height={100}
      />,
    );

    const [rising, falling] = bodies();
    expect(rising!.props.fill).toBe(colors.direction.long);
    expect(falling!.props.fill).toBe(colors.direction.short);
  });

  it('uses no gold anywhere', () => {
    render(
      <Candles
        candles={[bar()]}
        levels={{ stop: 63500, target: 65000 }}
        markers={[
          { at: Date.parse('2026-08-27T08:00:00Z'), price: 64100, direction: 'long', kind: 'entry' },
        ]}
        width={200}
        height={100}
      />,
    );

    const golds = new Set(Object.values(colors.gold));
    const used = screen.UNSAFE_root.findAll(() => true).flatMap((node) => {
      const props = node.props as { fill?: unknown; stroke?: unknown };
      return [props.fill, props.stroke].filter((v): v is string => typeof v === 'string');
    });

    for (const colour of used) {
      expect(golds.has(colour as never)).toBe(false);
    }
  });
});

describe('the levels', () => {
  it('draws the stop and the target when a signal is being looked at', () => {
    render(
      <Candles
        candles={[bar()]}
        levels={{ stop: 63500, target: 65000 }}
        width={200}
        height={100}
      />,
    );

    // Two horizontals plus one wick.
    const lines = screen.UNSAFE_root.findAll(
      (node) => typeof node.type !== 'string' && 'strokeDasharray' in (node.props ?? {}),
    );
    expect(lines.length).toBeGreaterThanOrEqual(2);
  });
});

describe('markers', () => {
  it('places an entry and an exit for a resolved signal', () => {
    const signal = {
      id: 'x',
      signal_time: '2026-08-27T08:00:00Z',
      direction: 'long',
      entry_price: '64100',
      signal_price: '64000',
    } as unknown as Signal;

    const outcome = {
      signal_id: 'x',
      resolved_at: '2026-08-27T16:00:00Z',
      resolved_price: '64800',
    } as unknown as Outcome;

    const markers = markersFor([signal], [outcome]);

    expect(markers).toHaveLength(2);
    expect(markers[0]).toMatchObject({ kind: 'entry', price: 64100 });
    expect(markers[1]).toMatchObject({ kind: 'exit', price: 64800 });
  });

  it('falls back to the reference price when the entry is not yet known', () => {
    // entry_price is null until the bar after the signal closes, and a signal
    // with no marker at all would be invisible on the chart in exactly the
    // window somebody is most likely to be looking at it.
    const signal = {
      id: 'x',
      signal_time: '2026-08-27T08:00:00Z',
      direction: 'short',
      entry_price: null,
      signal_price: '64000',
    } as unknown as Signal;

    expect(markersFor([signal], [])).toEqual([
      {
        at: Date.parse('2026-08-27T08:00:00Z'),
        price: 64000,
        direction: 'short',
        kind: 'entry',
      },
    ]);
  });

  it('places no exit for an outcome that has not resolved', () => {
    const signal = {
      id: 'x',
      signal_time: '2026-08-27T08:00:00Z',
      direction: 'long',
      entry_price: '64100',
      signal_price: '64000',
    } as unknown as Signal;

    const outcome = {
      signal_id: 'x',
      resolved_at: null,
      resolved_price: null,
    } as unknown as Outcome;

    expect(markersFor([signal], [outcome])).toHaveLength(1);
  });
});

describe('an empty chart', () => {
  it('renders nothing rather than dividing by zero', () => {
    render(<Candles candles={[]} width={200} height={100} />);
    expect(screen.UNSAFE_queryAllByType(Rect)).toHaveLength(0);
  });
});
