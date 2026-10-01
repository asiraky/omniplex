import { useEffect, useRef, useState } from "react";
import { AnimatePresence, MotionConfig, motion } from "motion/react";

const ACTIONS = ["Edit form.ts", "Run npm test", "Read total.ts", "Edit checkout.spec.ts", "Run git diff", "Edit validate.ts"];
const spring = { type: "spring", stiffness: 500, damping: 26 } as const;
const START = 3;
const KEEP = 4;

function Log({ upTo }: { upTo: number }) {
  const ids = [];
  for (let i = Math.max(0, upTo - KEEP); i < upTo; i++) ids.push(i);
  return (
    <ul className="log">
      <AnimatePresence initial={false} mode="popLayout">
        {ids.map((i) => (
          <motion.li
            key={i}
            layout
            initial={{ x: -16, opacity: 0 }}
            animate={{ x: 0, opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={spring}
          >
            <span className="log-n">{i + 1}</span> {ACTIONS[i % ACTIONS.length]}
          </motion.li>
        ))}
      </AnimatePresence>
    </ul>
  );
}

/**
 * Kill the signal: the phone's view stops, the log on the machine does not,
 * and turning the signal back on replays what the phone missed.
 */
export default function Signal() {
  const [online, setOnline] = useState(true);
  const [log, setLog] = useState({ machine: START, phone: START });
  const [caught, setCaught] = useState(0);
  const live = useRef(online);
  live.current = online;
  const { machine, phone } = log;

  useEffect(() => {
    const timer = setInterval(() => {
      setLog((v) => ({ machine: v.machine + 1, phone: live.current ? v.machine + 1 : v.phone }));
    }, 1300);
    return () => clearInterval(timer);
  }, []);

  const toggle = () => {
    setCaught(online ? 0 : machine - phone);
    if (!online) setLog({ machine, phone: machine });
    setOnline(!online);
  };

  return (
    <MotionConfig reducedMotion="user">
      <section className="tile tile-signal bg-white" aria-labelledby="signal-title">
        <div className="signal-head">
          <div>
            <h2 id="signal-title">Kill the signal</h2>
            <p>Every session is written to a log on the machine. Drop the connection or close the tab and nothing is lost.</p>
          </div>
          <button type="button" role="switch" aria-checked={online} className="switch" data-on={online} onClick={toggle}>
            <span className="switch-text">{online ? "Signal on" : "Signal off"}</span>
            <span className="switch-track">
              <motion.span className="switch-knob" layout transition={spring} />
            </span>
          </button>
        </div>
        <div className="signal-cols" aria-hidden="true">
          <div className="signal-col">
            <p className="signal-label">Machine log</p>
            <Log upTo={machine} />
          </div>
          <div className="signal-col" data-off={!online}>
            <p className="signal-label">Your phone {online ? "" : "(no signal)"}</p>
            <Log upTo={phone} />
          </div>
        </div>
        <p className="state" aria-live="polite">
          {!online
            ? `Phone offline. The machine has written ${machine - phone} ${machine - phone === 1 ? "line" : "lines"} it has not seen.`
            : caught > 0
              ? `Back online. Caught up ${caught} ${caught === 1 ? "line" : "lines"} from the log.`
              : "Phone online. It shows what the log shows."}
        </p>
      </section>
    </MotionConfig>
  );
}
