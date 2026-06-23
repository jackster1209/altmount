import { AlertTriangle, ArrowRight, CheckCircle, Database, RefreshCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";

interface MaintenanceStatus {
	mode: "maintenance";
	source: string;
	target: string;
	reason: string;
	source_total: number;
	source_counts: Record<string, number>;
	source_error?: string;
	status: "ready" | "running" | "done" | "error";
	progress: string[];
	error: string;
}

async function fetchStatus(): Promise<MaintenanceStatus | null> {
	try {
		const res = await fetch("/api/maintenance/status", { cache: "no-store" });
		if (!res.ok) return null;
		return res.json();
	} catch {
		return null;
	}
}

async function startMigration(): Promise<string | null> {
	try {
		const res = await fetch("/api/maintenance/run", {
			method: "POST",
			cache: "no-store",
		});
		const body = await res.json().catch(() => ({}));
		if (!res.ok) {
			const msg =
				(typeof body?.error === "object" ? body?.error?.message : body?.error) ||
				body?.message ||
				`HTTP ${res.status}`;
			return msg;
		}
		return null;
	} catch (err) {
		return err instanceof Error ? err.message : "Failed to start migration";
	}
}

export function MaintenancePage() {
	const [status, setStatus] = useState<MaintenanceStatus | null>(null);
	const [startError, setStartError] = useState<string | null>(null);
	const [isStarting, setIsStarting] = useState(false);
	const progressRef = useRef<HTMLDivElement>(null);

	useEffect(() => {
		let active = true;

		const poll = async () => {
			const data = await fetchStatus();
			if (active && data) {
				setStatus(data);
			}
		};

		poll();
		const interval = setInterval(poll, 2000);
		return () => {
			active = false;
			clearInterval(interval);
		};
	}, []);

	// Auto-scroll progress log to bottom
	useEffect(() => {
		if (progressRef.current) {
			progressRef.current.scrollTop = progressRef.current.scrollHeight;
		}
	}, [status?.progress]);

	const handleStart = async () => {
		setIsStarting(true);
		setStartError(null);
		const err = await startMigration();
		if (err) {
			setStartError(err);
		}
		setIsStarting(false);
	};

	const isRunning = status?.status === "running";
	const isDone = status?.status === "done";
	const isError = status?.status === "error";
	const isReady = status?.status === "ready";

	return (
		<div className="flex min-h-screen flex-col items-center justify-center bg-base-200 p-4">
			<div className="w-full max-w-2xl space-y-6">
				{/* Header */}
				<div className="text-center">
					<div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-warning/10">
						<Database className="h-8 w-8 text-warning" />
					</div>
					<h1 className="font-bold text-3xl tracking-tight">Database Migration</h1>
					<p className="mt-2 text-base-content/60 text-sm">
						AltMount is in maintenance mode while your data is migrated to the new backend.
					</p>
				</div>

				{/* Migration plan card */}
				{status && (
					<div className="card border-2 border-base-300/50 bg-base-100 shadow-md">
						<div className="card-body space-y-6 p-6">
							{/* Source → Target */}
							<div className="flex items-center justify-center gap-4">
								<div className="rounded-xl border border-base-300 bg-base-200 px-4 py-2 text-center">
									<div className="text-base-content/50 text-xs uppercase tracking-widest">
										From
									</div>
									<div className="font-bold text-lg capitalize">{status.source}</div>
								</div>
								<ArrowRight className="h-6 w-6 text-primary" />
								<div className="rounded-xl border border-primary/30 bg-primary/5 px-4 py-2 text-center">
									<div className="text-base-content/50 text-xs uppercase tracking-widest">
										To
									</div>
									<div className="font-bold text-lg capitalize">{status.target}</div>
								</div>
							</div>

							{/* Reason */}
							<div className="rounded-xl border border-base-300 bg-base-200/60 p-3">
								<div className="text-base-content/50 text-xs uppercase tracking-widest">
									Reason
								</div>
								<div className="mt-1 text-sm">{status.reason}</div>
							</div>

							{/* Row counts */}
							{status.source_total > 0 && (
								<div>
									<div className="mb-2 text-base-content/50 text-xs uppercase tracking-widest">
										Data to migrate ({status.source_total.toLocaleString()} total rows)
									</div>
									<div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
										{Object.entries(status.source_counts).map(([table, count]) => (
											<div
												key={table}
												className="rounded-lg border border-base-300 bg-base-200/60 p-3 text-center"
											>
												<div className="font-bold text-lg">{count.toLocaleString()}</div>
												<div className="text-base-content/50 text-xs capitalize">
													{table.replace(/_/g, " ")}
												</div>
											</div>
										))}
									</div>
								</div>
							)}

							{status.source_error && (
								<div className="alert alert-warning py-2 text-sm">
									<AlertTriangle className="h-4 w-4" />
									<span>Could not read source row counts: {status.source_error}</span>
								</div>
							)}
						</div>
					</div>
				)}

				{/* State-specific UI */}
				{isReady && (
					<div className="card border-2 border-base-300/50 bg-base-100 shadow-md">
						<div className="card-body items-center p-6 text-center">
							<p className="mb-4 text-base-content/70 text-sm">
								The migration has not started yet. Click the button below to begin. This may
								take several minutes depending on the amount of data.
							</p>
							{startError && (
								<div className="alert alert-error mb-4 py-2 text-sm">
									<span>{startError}</span>
								</div>
							)}
							<button
								type="button"
								className="btn btn-primary px-10"
								onClick={handleStart}
								disabled={isStarting}
							>
								{isStarting ? (
									<span className="loading loading-spinner loading-sm" />
								) : (
									<Database className="h-5 w-5" />
								)}
								{isStarting ? "Starting..." : "Run Migration"}
							</button>
						</div>
					</div>
				)}

				{/* Progress log — shown while running or after completion */}
				{(isRunning || isDone || isError) && status && (
					<div className="card border-2 border-base-300/50 bg-base-100 shadow-md">
						<div className="card-body p-6">
							<div className="mb-3 flex items-center gap-2">
								{isRunning && (
									<span className="loading loading-spinner loading-sm text-primary" />
								)}
								{isDone && <CheckCircle className="h-5 w-5 text-success" />}
								{isError && <AlertTriangle className="h-5 w-5 text-error" />}
								<span className="font-semibold text-sm">
									{isRunning && "Migration in progress…"}
									{isDone && "Migration complete"}
									{isError && "Migration failed"}
								</span>
							</div>

							{status.progress.length > 0 && (
								<div
									ref={progressRef}
									className="max-h-64 overflow-y-auto rounded-xl border border-base-300 bg-base-900 p-4 font-mono text-xs"
									style={{ backgroundColor: "hsl(var(--b3))" }}
								>
									{status.progress.map((line, i) => (
										// biome-ignore lint/suspicious/noArrayIndexKey: stable log lines
										<div key={i} className="leading-relaxed text-base-content/80">
											{line}
										</div>
									))}
								</div>
							)}

							{isError && status.error && (
								<div className="alert alert-error mt-3 py-2 text-sm">
									<AlertTriangle className="h-4 w-4 shrink-0" />
									<span>{status.error}</span>
								</div>
							)}

							{isDone && (
								<div className="mt-4 rounded-xl border border-success/30 bg-success/5 p-4 text-center">
									<CheckCircle className="mx-auto mb-2 h-8 w-8 text-success" />
									<div className="font-bold text-success">Migration successful!</div>
									<div className="mt-1 text-sm text-base-content/60">
										Restart the server to complete the switch to{" "}
										<strong className="capitalize">{status.target}</strong>.
									</div>
									<button
										type="button"
										className="btn btn-success btn-sm mt-4"
										onClick={() => window.location.reload()}
									>
										<RefreshCw className="h-4 w-4" />
										Reload page
									</button>
								</div>
							)}
						</div>
					</div>
				)}

				{!status && (
					<div className="flex justify-center py-8">
						<span className="loading loading-spinner loading-lg text-primary" />
					</div>
				)}
			</div>
		</div>
	);
}
