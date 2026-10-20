import type { ScanTone } from "./outcome-copy";

// How the phone signals a scan's result beside the written line. A phone that vibrates (Android)
// buzzes: once for success, three times for anything to look at. One that cannot (iPhone: Safari
// has no vibration) beeps through Web Audio, high for success and low otherwise, and flashes the
// screen green or red. Every signal is best effort: a muted phone or a refused AudioContext only
// loses the extra cue, never the line.

export const VIBRATION: Readonly<Record<ScanTone, number | number[]>> = { ok: 80, error: [90, 70, 90, 70, 90] };

interface Beep {
  hz: number;
  seconds: number;
  wave: OscillatorType;
}

export const BEEPS: Readonly<Record<ScanTone, Beep>> = {
  ok: { hz: 1_200, seconds: 0.12, wave: "sine" },
  error: { hz: 300, seconds: 0.3, wave: "square" },
};

const PEAK_GAIN = 0.25;
const SILENT_GAIN = 0.0001;

export interface Feedback {
  /** Call inside a tap: Safari only lets a page play sound after the person touched it. */
  unlock(): void;
  signal(tone: ScanTone): void;
}

export interface FeedbackOptions {
  /** navigator.vibrate bound to navigator, or null where it does not exist. */
  vibrate?: ((pattern: number | number[]) => boolean) | null;
  createAudioContext?: (() => AudioContext) | null;
  /** Flashes the screen in the tone's colour. */
  flash: (tone: ScanTone) => void;
}

function defaultVibrate(): FeedbackOptions["vibrate"] {
  return typeof navigator !== "undefined" && typeof navigator.vibrate === "function" ? (pattern) => navigator.vibrate(pattern) : null;
}

function defaultAudioContext(): FeedbackOptions["createAudioContext"] {
  // Safari before 14.1 only has the prefixed constructor.
  const g = globalThis as { AudioContext?: typeof AudioContext; webkitAudioContext?: typeof AudioContext };
  const Ctor = g.AudioContext ?? g.webkitAudioContext;
  return Ctor ? () => new Ctor() : null;
}

export function createFeedback({ vibrate = defaultVibrate(), createAudioContext = defaultAudioContext(), flash }: FeedbackOptions): Feedback {
  let audio: AudioContext | null = null;

  const context = (): AudioContext | null => {
    if (audio === null && createAudioContext) {
      try {
        audio = createAudioContext();
      } catch {
        return null;
      }
    }
    if (audio?.state === "suspended") void audio.resume().catch(() => undefined);
    return audio;
  };

  const tone = (ctx: AudioContext, { hz, seconds, wave }: Beep, peak: number) => {
    const at = ctx.currentTime;
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.type = wave;
    osc.frequency.setValueAtTime(hz, at);
    gain.gain.setValueAtTime(SILENT_GAIN, at);
    gain.gain.exponentialRampToValueAtTime(peak, at + 0.01);
    gain.gain.exponentialRampToValueAtTime(SILENT_GAIN, at + seconds);
    osc.connect(gain).connect(ctx.destination);
    osc.start(at);
    osc.stop(at + seconds + 0.02);
  };

  /** True when the phone took the vibration (false where it cannot, or refused it). */
  const buzz = (t: ScanTone): boolean => {
    try {
      return vibrate?.(VIBRATION[t]) ?? false;
    } catch {
      return false;
    }
  };

  return {
    unlock() {
      try {
        const ctx = context();
        // A silent blip started inside the tap is what unlocks audio on iOS.
        if (ctx) tone(ctx, BEEPS.ok, SILENT_GAIN);
      } catch {
        // Sound stays off; vibration, the flash and the line still work.
      }
    },
    signal(t) {
      if (buzz(t)) return;
      try {
        const ctx = context();
        if (ctx) tone(ctx, BEEPS[t], PEAK_GAIN);
      } catch {
        // A refused or closed AudioContext: the flash and the line remain.
      }
      flash(t);
    },
  };
}
