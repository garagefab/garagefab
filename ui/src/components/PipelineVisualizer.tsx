import { Check, Loader2, AlertCircle, Clock, FileText, ChevronRight } from 'lucide-react';
import { Job, StepRun } from '../types/api';

interface PipelineVisualizerProps {
  job: Job;
  stepRuns: StepRun[];
  onOpenLogs?: (step: StepRun) => void;
}

interface StepNodeDef {
  key: string;
  stage: string;
  label: string;
  description: string;
}

// Stage sequence mapping for profiles
function getStagesForWorkType(workType: string): StepNodeDef[] {
  switch (workType) {
    case 'feature':
      return [
        { key: 'intent', stage: '01_Intent', label: 'Intent', description: 'Intake & checkpoint' },
        { key: 'spec', stage: '02_Clarification_and_Spec', label: 'Spec & Q&A', description: 'Draft spec & questions' },
        { key: 'coding', stage: '04_Coding', label: 'Coding', description: 'Agent & repair loop' },
        { key: 'review', stage: '05_Independent_Review', label: 'Review', description: 'Independent review report' },
        { key: 'approval', stage: '06_Human_Approval_Gate', label: 'Approval Gate', description: 'Human sign-off' },
        { key: 'done', stage: '07_Done', label: 'Delivered', description: 'Completed' },
      ];
    case 'bug_fix':
      return [
        { key: 'intent', stage: '01_Intent', label: 'Intent', description: 'Intake' },
        { key: 'probe', stage: '03_Failing_Probe', label: 'Failing Probe', description: 'Reproduction test' },
        { key: 'coding', stage: '04_Coding', label: 'Coding', description: 'Agent repair' },
        { key: 'review', stage: '05_Independent_Review', label: 'Review', description: 'Independent review' },
        { key: 'approval', stage: '06_Human_Approval_Gate', label: 'Approval Gate', description: 'Human sign-off' },
        { key: 'done', stage: '07_Done', label: 'Delivered', description: 'Completed' },
      ];
    case 'refactor':
    case 'docs':
    default:
      return [
        { key: 'intent', stage: '01_Intent', label: 'Intent', description: 'Intake' },
        { key: 'coding', stage: '04_Coding', label: 'Coding', description: 'Agent coding' },
        { key: 'review', stage: '05_Independent_Review', label: 'Review', description: 'Review report' },
        { key: 'approval', stage: '06_Human_Approval_Gate', label: 'Approval Gate', description: 'Human sign-off' },
        { key: 'done', stage: '07_Done', label: 'Delivered', description: 'Completed' },
      ];
  }
}

export function PipelineVisualizer({ job, stepRuns, onOpenLogs }: PipelineVisualizerProps) {
  const steps = getStagesForWorkType(job.work_type);

  // Find index of current stage
  const currentStageIndex = steps.findIndex((s) => s.stage === job.stage);

  return (
    <div className="rounded-2xl border border-slate-800 bg-slate-900/60 p-5 shadow-md">
      <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-4">
        SDLC Pipeline Progression
      </h3>

      <div className="flex flex-col md:flex-row items-stretch md:items-center justify-between gap-3 overflow-x-auto pb-2">
        {steps.map((step, idx) => {
          // Determine status for this step node
          let state: 'completed' | 'running' | 'failed' | 'waiting' | 'pending' = 'pending';

          if (job.status === 'done') {
            state = 'completed';
          } else if (idx < currentStageIndex) {
            state = 'completed';
          } else if (idx === currentStageIndex) {
            if (job.status === 'running') {
              state = 'running';
            } else if (job.status === 'failed' || job.status === 'interrupted') {
              state = 'failed';
            } else if (
              job.status === 'needs_clarification' ||
              job.status === 'spec_review' ||
              job.status === 'awaiting_approval'
            ) {
              state = 'waiting';
            } else {
              state = 'running';
            }
          } else {
            state = 'pending';
          }

          // Check if there is an associated step run record
          const matchingStepRun = stepRuns.find((sr) => sr.stage === step.stage);

          return (
            <div key={step.key} className="flex-1 flex items-center min-w-[140px]">
              <div
                className={`flex-1 rounded-xl border p-3 transition-all ${
                  state === 'completed'
                    ? 'border-emerald-500/30 bg-emerald-500/5'
                    : state === 'running'
                    ? 'border-blue-500/50 bg-blue-500/10 ring-2 ring-blue-500/20 shadow-md shadow-blue-500/10'
                    : state === 'waiting'
                    ? 'border-amber-500/40 bg-amber-500/10 ring-2 ring-amber-500/20'
                    : state === 'failed'
                    ? 'border-rose-500/40 bg-rose-500/10 ring-2 ring-rose-500/20'
                    : 'border-slate-800 bg-slate-950/40 opacity-70'
                }`}
              >
                <div className="flex items-center justify-between gap-1 mb-1.5">
                  <span className="text-xs font-semibold text-white truncate">{step.label}</span>

                  {state === 'completed' && <Check className="h-3.5 w-3.5 text-emerald-400 shrink-0" />}
                  {state === 'running' && (
                    <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-400 shrink-0" />
                  )}
                  {state === 'waiting' && (
                    <Clock className="h-3.5 w-3.5 text-amber-400 shrink-0 animate-pulse" />
                  )}
                  {state === 'failed' && <AlertCircle className="h-3.5 w-3.5 text-rose-400 shrink-0" />}
                  {state === 'pending' && <span className="h-2 w-2 rounded-full bg-slate-700 shrink-0" />}
                </div>

                <p className="text-[11px] text-slate-400 truncate">{step.description}</p>

                {/* On-demand View Logs link (UI-7, LOG-5) */}
                {matchingStepRun && onOpenLogs && (
                  <button
                    type="button"
                    onClick={() => onOpenLogs(matchingStepRun)}
                    className="mt-2.5 inline-flex items-center gap-1 text-[11px] font-mono text-indigo-400 hover:text-indigo-300 transition-colors cursor-pointer"
                  >
                    <FileText className="h-3 w-3" />
                    <span>View Logs</span>
                  </button>
                )}
              </div>

              {idx < steps.length - 1 && (
                <ChevronRight className="hidden md:block h-4 w-4 text-slate-700 shrink-0 mx-1" />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
