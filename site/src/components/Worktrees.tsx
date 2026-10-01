import { useRef, useState } from "react";
import { AnimatePresence, MotionConfig, motion } from "motion/react";

const NAMES = ["fix-safari-checkout", "cart-totals", "upgrade-deps", "flaky-login-test", "dark-mode", "invoice-pdf"];
const spring = { type: "spring", stiffness: 480, damping: 24 } as const;

/** Threads you can add and finish. Each one gets a worktree that goes away with it. */
export default function Worktrees() {
  const [threads, setThreads] = useState([0, 1]);
  const next = useRef(2);
  const [note, setNote] = useState("Two threads, two worktrees, one repo.");

  const add = () => {
    const id = next.current++;
    setThreads([...threads, id]);
    setNote(`Created worktrees/${NAMES[id % NAMES.length]}.`);
  };
  const finish = (id: number) => {
    setThreads(threads.filter((t) => t !== id));
    setNote(`Cleaned up worktrees/${NAMES[id % NAMES.length]}.`);
  };

  return (
    <MotionConfig reducedMotion="user">
      <section className="tile tile-trees bg-blue" aria-labelledby="trees-title">
        <h2 id="trees-title">One worktree per thread</h2>
        <p>Each thread can get its own git worktree. Omniplex creates it and cleans it up.</p>
        <ul className="threads">
          <AnimatePresence initial={false} mode="popLayout">
            {threads.map((id) => (
              <motion.li
                key={id}
                layout
                initial={{ scale: 0.6, opacity: 0 }}
                animate={{ scale: 1, opacity: 1 }}
                exit={{ scale: 0.6, opacity: 0 }}
                transition={spring}
              >
                <span className="thread-name">{NAMES[id % NAMES.length]}</span>
                <button type="button" onClick={() => finish(id)} aria-label={`Finish ${NAMES[id % NAMES.length]}`}>
                  done
                </button>
              </motion.li>
            ))}
          </AnimatePresence>
        </ul>
        <motion.button type="button" className="press" whileTap={{ scale: 0.92 }} onClick={add} disabled={threads.length >= 5}>
          New thread
        </motion.button>
        <p className="state" aria-live="polite">
          {note}
        </p>
        <p className="small">A project can span several repos or folders. Threads are grouped by project and carry labels.</p>
      </section>
    </MotionConfig>
  );
}
