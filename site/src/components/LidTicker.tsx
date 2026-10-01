import { useEffect, useState } from "react";
import { AnimatePresence, MotionConfig, motion } from "motion/react";

const ACTIONS = [
  "Edit src/checkout/form.ts",
  "Run npm test",
  "Read src/cart/total.ts",
  "Edit tests/checkout.spec.ts",
  "Run git diff",
];
const spring = { type: "spring", stiffness: 420, damping: 22 } as const;

/**
 * Two tiles that share one fact: the laptop is a screen, the work is elsewhere.
 * Click the lid shut in the first tile and the counter in the second keeps going.
 */
export default function LidTicker() {
  const [shut, setShut] = useState(false);
  const [lines, setLines] = useState(1204);
  const [step, setStep] = useState(0);

  useEffect(() => {
    const timer = setInterval(() => {
      setLines((v) => v + 1 + Math.floor(Math.random() * 3));
      setStep((v) => v + 1);
    }, 900);
    return () => clearInterval(timer);
  }, []);

  return (
    <MotionConfig reducedMotion="user">
      <section className="tile tile-lid bg-pink" aria-labelledby="lid-title">
        <h2 id="lid-title">Shut the lid</h2>
        <p>The laptop is a screen onto another machine. Go on, close it.</p>
        <motion.button
          type="button"
          className="laptop"
          aria-pressed={shut}
          onClick={() => setShut(!shut)}
          whileTap={{ scale: 0.96 }}
          transition={spring}
        >
          <span className="laptop-hinge">
            <motion.span
              className="laptop-lid"
              initial={false}
              animate={{ rotateX: shut ? -86 : 0 }}
              transition={{ type: "spring", stiffness: 260, damping: 16 }}
            >
              <span className="laptop-line" />
              <span className="laptop-line short" />
              <span className="laptop-line" />
            </motion.span>
          </span>
          <span className="laptop-base" />
          <span className="laptop-label">{shut ? "Open it again" : "Click to close"}</span>
        </motion.button>
        <p className="state" aria-live="polite">
          {shut ? "Lid shut. Nobody is watching. Check the counter." : "Lid open. You are watching."}
        </p>
      </section>

      <section className="tile tile-ticker bg-black" aria-labelledby="ticker-title">
        <h2 id="ticker-title">Meanwhile, on the machine you left on</h2>
        <p className="ticker-num" aria-hidden="true">
          {lines.toLocaleString("en-US")}
        </p>
        <p className="ticker-unit">lines in the session log, and counting</p>
        <p className="ticker-now" aria-hidden="true">
          <AnimatePresence mode="popLayout" initial={false}>
            <motion.span
              key={step}
              initial={{ y: 14, opacity: 0 }}
              animate={{ y: 0, opacity: 1 }}
              exit={{ y: -14, opacity: 0 }}
              transition={spring}
            >
              {ACTIONS[step % ACTIONS.length]}
            </motion.span>
          </AnimatePresence>
        </p>
        <AnimatePresence>
          {shut && (
            <motion.span
              className="sticker sticker-still"
              initial={{ scale: 0, rotate: -30 }}
              animate={{ scale: 1, rotate: 8 }}
              exit={{ scale: 0, rotate: 30 }}
              transition={{ type: "spring", stiffness: 380, damping: 14 }}
            >
              lid shut, still working
            </motion.span>
          )}
        </AnimatePresence>
        <p className="small">
          The agents run here: a desktop, a Mac mini or a server. A laptop works as the host too, until it sleeps.
        </p>
      </section>
    </MotionConfig>
  );
}
