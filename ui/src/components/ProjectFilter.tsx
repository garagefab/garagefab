import { Project } from '../types/api';
import { Filter } from 'lucide-react';

interface ProjectFilterProps {
  projects: Project[];
  selectedProjectId: number | null;
  onSelectProject: (id: number | null) => void;
}

export function ProjectFilter({
  projects,
  selectedProjectId,
  onSelectProject,
}: ProjectFilterProps) {
  return (
    <div className="flex items-center gap-2">
      <Filter className="h-4 w-4 text-slate-400" />
      <select
        aria-label="Filter by project"
        value={selectedProjectId === null ? '' : selectedProjectId}
        onChange={(e) => {
          const val = e.target.value;
          onSelectProject(val === '' ? null : Number(val));
        }}
        className="rounded-xl border border-slate-700 bg-slate-900 px-3 py-1.5 text-xs text-white focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 transition-colors cursor-pointer"
      >
        <option value="">All Projects</option>
        {projects
          .filter((p) => !p.is_archived)
          .map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
      </select>
    </div>
  );
}
