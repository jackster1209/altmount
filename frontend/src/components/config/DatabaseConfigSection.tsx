import { AlertTriangle, Database, HardDrive, Save, Wifi } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { ConfigResponse, DatabaseConfig } from "../../types/config";
import { LoadingSpinner } from "../ui/LoadingSpinner";

interface DatabaseConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: DatabaseConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

export function DatabaseConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: DatabaseConfigSectionProps) {
	const [formData, setFormData] = useState<DatabaseConfig>({ ...config.database });
	const [hasChanges, setHasChanges] = useState(false);
	const [isTesting, setIsTesting] = useState(false);
	const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null);

	useEffect(() => {
		setFormData({ ...config.database });
		setHasChanges(false);
		setTestResult(null);
	}, [config.database]);

	const handleChange = (field: keyof DatabaseConfig, value: string) => {
		const next = { ...formData, [field]: value };
		setFormData(next);
		setHasChanges(JSON.stringify(next) !== JSON.stringify(config.database));
		setTestResult(null);
	};

	const handleTest = useCallback(async () => {
		setIsTesting(true);
		setTestResult(null);
		try {
			const res = await fetch("/api/config/database/test-connection", {
				method: "POST",
				credentials: "include",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({
					type: formData.type || "sqlite",
					path: formData.path,
					dsn: formData.dsn,
				}),
			});
			if (!res.ok) {
				const body = await res.json().catch(() => ({}));
				const msg =
					(typeof body?.error === "object" ? body?.error?.message : body?.error) ||
					body?.message ||
					`HTTP ${res.status}`;
				throw new Error(msg);
			}
			setTestResult({ ok: true, message: "Connection successful" });
		} catch (err) {
			setTestResult({
				ok: false,
				message: err instanceof Error ? err.message : "Connection failed",
			});
		} finally {
			setIsTesting(false);
		}
	}, [formData]);

	const handleSave = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("database", formData);
		setHasChanges(false);
	};

	const dbType = formData.type || "sqlite";
	const isPostgres = dbType === "postgres";

	return (
		<div className="space-y-10">
			<div className="space-y-8">
				{/* Backend Selection */}
				<div className="space-y-6 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="flex items-center gap-2">
						<HardDrive className="h-4 w-4 text-base-content/60" />
						<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
							Storage Backend
						</h4>
						<div className="h-px flex-1 bg-base-300/50" />
					</div>

					<fieldset className="fieldset">
						<legend className="fieldset-legend font-semibold">Database Type</legend>
						<select
							className="select w-full max-w-xs"
							value={dbType}
							disabled={isReadOnly}
							onChange={(e) => handleChange("type", e.target.value)}
						>
							<option value="sqlite">SQLite (default)</option>
							<option value="postgres">PostgreSQL</option>
						</select>
						<p className="label mt-2 text-base-content/70 text-xs">
							Changing the database backend requires a server restart and will trigger a
							data migration on next startup.
						</p>
					</fieldset>
				</div>

				{/* Connection Settings */}
				<div className="space-y-6 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="flex items-center gap-2">
						<Database className="h-4 w-4 text-base-content/60" />
						<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
							Connection Settings
						</h4>
						<div className="h-px flex-1 bg-base-300/50" />
					</div>

					{isPostgres ? (
						<fieldset className="fieldset">
							<legend className="fieldset-legend font-semibold">PostgreSQL DSN</legend>
							<input
								type="text"
								className="input input-bordered w-full bg-base-100 font-mono text-sm"
								value={formData.dsn}
								readOnly={isReadOnly}
								placeholder="postgres://user:password@host:5432/dbname?sslmode=disable"
								onChange={(e) => handleChange("dsn", e.target.value)}
							/>
							<p className="label mt-2 text-base-content/70 text-xs">
								Full PostgreSQL connection string.
							</p>
						</fieldset>
					) : (
						<fieldset className="fieldset">
							<legend className="fieldset-legend font-semibold">SQLite Database Path</legend>
							<input
								type="text"
								className="input input-bordered w-full bg-base-100 font-mono text-sm"
								value={formData.path}
								readOnly={isReadOnly}
								placeholder="/data/altmount.db"
								onChange={(e) => handleChange("path", e.target.value)}
							/>
							<p className="label mt-2 text-base-content/70 text-xs">
								Path to the SQLite database file.
							</p>
						</fieldset>
					)}

					{!isReadOnly && (
						<div className="flex flex-col gap-3">
							<button
								type="button"
								className="btn btn-outline btn-sm w-fit"
								onClick={handleTest}
								disabled={isTesting || (isPostgres && !formData.dsn)}
							>
								{isTesting ? <LoadingSpinner size="sm" /> : <Wifi className="h-4 w-4" />}
								{isTesting ? "Testing..." : "Test Connection"}
							</button>

							{testResult && (
								<div className={`alert py-2 ${testResult.ok ? "alert-success" : "alert-error"}`}>
									<span className="text-sm">{testResult.message}</span>
								</div>
							)}
						</div>
					)}
				</div>

				{/* Restart warning when backend type changes */}
				{hasChanges && formData.type !== config.database.type && (
					<div className="alert alert-warning rounded-xl">
						<AlertTriangle className="h-5 w-5" />
						<div>
							<div className="font-bold text-sm">Restart and migration required</div>
							<div className="text-xs">
								Switching from <strong>{config.database.type || "sqlite"}</strong> to{" "}
								<strong>{formData.type}</strong> will trigger an automatic data migration on the
								next startup. The server will enter maintenance mode until the migration
								completes.
							</div>
						</div>
					</div>
				)}
			</div>

			{!isReadOnly && (
				<div className="flex justify-end border-base-200 border-t pt-4">
					<button
						type="button"
						className={`btn btn-primary px-10 shadow-lg shadow-primary/20 ${!hasChanges && "btn-ghost border-base-300"}`}
						onClick={handleSave}
						disabled={!hasChanges || isUpdating}
					>
						{isUpdating ? <LoadingSpinner size="sm" /> : <Save className="h-4 w-4" />}
						{isUpdating ? "Saving..." : "Save Changes"}
					</button>
				</div>
			)}
		</div>
	);
}
