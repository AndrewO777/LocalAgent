import type { Skill } from '../types';

interface Props {
  catalog: Skill[];
  selected: string[];
  slashSkills: string[]; // names activated via /skill-name in the goal (locked on)
  onToggle: (name: string) => void;
  effectiveCount: number;
}

// SkillsBlock renders the discovered skill catalog as checkboxes. Skills
// activated via /slash-name in the goal text show as locked-on (the
// checkbox is forced checked + disabled).
export function SkillsBlock({ catalog, selected, slashSkills, onToggle, effectiveCount }: Props) {
  return (
    <div className="bg-panel-2 border border-border rounded-md px-2.5 py-2 mb-3.5">
      <div className="flex justify-between items-center mb-1.5">
        <span className="text-[11px] font-semibold text-muted uppercase tracking-wider">
          Skills {effectiveCount > 0 && `· ${effectiveCount} active`}
        </span>
        <span className="text-[10px] text-muted normal-case">
          SKILL.md in ~/.localagent/skills or workdir/.localagent/skills
        </span>
      </div>

      {catalog.length === 0 ? (
        <div className="text-muted text-xs italic">
          No skills discovered. Drop a <code className="text-fg">SKILL.md</code> into{' '}
          <code className="text-fg">~/.localagent/skills/&lt;name&gt;/</code> or{' '}
          <code className="text-fg">&lt;workdir&gt;/.localagent/skills/&lt;name&gt;/</code>.
        </div>
      ) : (
        catalog.map((s) => {
          const viaSlash = slashSkills.includes(s.name);
          const checked = selected.includes(s.name) || viaSlash;
          return (
            <label
              key={s.name}
              className="flex items-start gap-2 py-1 cursor-pointer"
              title={s.path}
            >
              <input
                type="checkbox"
                className="mt-0.5"
                checked={checked}
                disabled={viaSlash}
                onChange={() => onToggle(s.name)}
              />
              <div className="flex-1">
                <div>
                  <span className="font-semibold">{s.name}</span>
                  <span className="ml-1.5 text-[10px] text-muted px-1 border border-border rounded uppercase">
                    {s.source}
                  </span>
                  {viaSlash && (
                    <span className="ml-1.5 text-[11px] text-accent font-normal">· /slash</span>
                  )}
                </div>
                <div className="text-muted text-xs leading-snug">{s.description}</div>
              </div>
            </label>
          );
        })
      )}
    </div>
  );
}
