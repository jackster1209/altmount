import {
	AlertTriangle,
	Check,
	Copy,
	HardDrive,
	Monitor,
	Palette,
	RefreshCw,
	Save,
	ShieldCheck,
	Terminal,
	Wifi,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import { useConfirm } from "../../contexts/ModalContext";
import { useToast } from "../../contexts/ToastContext";
import { useRegenerateAPIKey } from "../../hooks/useAuth";
import { copyToClipboard } from "../../lib/utils";
import type { ConfigResponse, DatabaseConfig, LogFormData } from "../../types/config";
import { LoadingSpinner } from "../ui/LoadingSpinner";
import { UpdateSection } from "./UpdateSection";

const LIGHT_THEMES = [
	"retro",
	"light",
	"cupcake",
	"bumblebee",
	"emerald",
	"corporate",
	"garden",
	"lofi",
	"pastel",
	"fantasy",
	"wireframe",
	"cmyk",
	"autumn",
	"lemonade",
	"winter",
	"nord",
	"caramellatte",
] as const;

const DARK_THEMES = [
	"forest",
	"dark",
	"dim",
	"dracula",
	"synthwave",
	"halloween",
	"luxury",
	"night",
	"coffee",
	"business",
	"sunset",
	"abyss",
] as const;

const MINIMAL_THEMES = ["glass-light", "glass-dark"] as const;

type ThemeId =
	| (typeof LIGHT_THEMES)[number]
	| (typeof DARK_THEMES)[number]
	| (typeof MINIMAL_THEMES)[number];

function getActiveTheme(): ThemeId | "system" {
	const saved = localStorage.getItem("theme");
	if (!saved) return "system";
	return saved as ThemeId;
}

function applyTheme(theme: ThemeId | "system") {
	if (theme === "system") {
		localStorage.removeItem("theme");
		const prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
		document.documentElement.setAttribute("data-theme", prefersDark ? "business" : "retro");
	} else {
		localStorage.setItem("theme", theme);
		document.documentElement.setAttribute("data-theme", theme);
	}
}

function ThemeSwatch({
	theme,
	label,
	isActive,
	onClick,
	minimal = false,
}: {
	theme: ThemeId;
	label?: string;
	isActive: boolean;
	onClick: () => void;
	// Minimal themes share one blue; show success/warning to preview green/amber.
	minimal?: boolean;
}) {
	return (
		<button
			type="button"
			className={`group relative flex cursor-pointer flex-col overflow-hidden rounded-lg border-2 transition-all ${
				isActive
					? "border-primary ring-2 ring-primary/30"
					: "border-base-300 hover:border-primary/50"
			}`}
			data-theme={theme}
			onClick={onClick}
		>
			<div className="flex h-8 w-full">
				<div className="flex-1 bg-primary" />
				<div className={`flex-1 ${minimal ? "bg-success" : "bg-secondary"}`} />
				<div className={`flex-1 ${minimal ? "bg-warning" : "bg-accent"}`} />
				<div className="flex-1 bg-neutral" />
			</div>
			<div className="flex w-full items-center justify-between bg-base-100 px-2 py-1.5">
				<span className="truncate text-[10px] text-base-content">{label ?? theme}</span>
				{isActive && <Check className="h-3 w-3 shrink-0 text-primary" />}
			</div>
		</button>
	);
}

interface SystemConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: LogFormData) => Promise<void>;
	onRefresh?: () => Promise<void>;
	onRestartRequired?: (configName: string) => void;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

export function SystemConfigSection({
	config,
	onUpdate,
	onRefresh,
	onRestartRequired,
	isReadOnly = false,
	isUpdating = false,
}: SystemConfigSectionProps) {
	const [formData, setFormData] = useState<LogFormData>({
		file: config.log.file,
		level: config.log.level,
		max_size: config.log.max_size,
		max_age: config.log.max_age,
		max_backups: config.log.max_backups,
		compress: config.log.compress,
	});
	const [profilerEnabled, setProfilerEnabled] = useState(config.profiler_enabled);
	const [hasChanges, setHasChanges] = useState(false);

	// Database sub-section state
	const [dbFormData, setDbFormData] = useState<DatabaseConfig>({
		type: config.database.type,
		path: config.database.path,
		dsn: config.database.dsn,
	});
	const [dbHasChanges, setDbHasChanges] = useState(false);
	const [dbIsTesting, setDbIsTesting] = useState(false);
	const [dbIsSaving, setDbIsSaving] = useState(false);
	const [dbTestResult, setDbTestResult] = useState<{
		status: "ok" | "new" | "error";
		message: string;
	} | null>(null);

	const regenerateAPIKey = useRegenerateAPIKey();
	const { confirmAction } = useConfirm();
	const { showToast } = useToast();

	useEffect(() => {
		const newFormData = {
			file: config.log.file,
			level: config.log.level,
			max_size: config.log.max_size,
			max_age: config.log.max_age,
			max_backups: config.log.max_backups,
			compress: config.log.compress,
		};
		setFormData(newFormData);
		setProfilerEnabled(config.profiler_enabled);
		setHasChanges(false);
	}, [config.log, config.profiler_enabled]);

	useEffect(() => {
		setDbFormData({
			type: config.database.type,
			path: config.database.path,
			dsn: config.database.dsn,
		});
		setDbHasChanges(false);
		setDbTestResult(null);
	}, [config.database]);

	const handleDbChange = (field: keyof DatabaseConfig, value: string) => {
		const next = { ...dbFormData, [field]: value };
		setDbFormData(next);
		setDbHasChanges(
			next.type !== config.database.type ||
				next.path !== config.database.path ||
				next.dsn !== config.database.dsn,
		);
		setDbTestResult(null);
	};

	const handleDbTest = useCallback(async () => {
		setDbIsTesting(true);
		setDbTestResult(null);
		try {
			const res = await fetch("/api/config/database/test-connection", {
				method: "POST",
				credentials: "include",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({
					type: dbFormData.type || "sqlite",
					path: dbFormData.path,
					dsn: dbFormData.dsn,
				}),
			});
			const body = await res.json().catch(() => ({}));
			if (!res.ok) {
				const msg =
					(typeof body?.error === "object" ? body?.error?.message : body?.error) ||
					body?.message ||
					`HTTP ${res.status}`;
				setDbTestResult({ status: "error", message: msg });
			} else {
				const data = body?.data ?? body;
				setDbTestResult({
					status: (data?.status as "ok" | "new") ?? "ok",
					message: data?.message ?? "connection successful",
				});
			}
		} catch (err) {
			setDbTestResult({
				status: "error",
				message: err instanceof Error ? err.message : "Connection failed",
			});
		} finally {
			setDbIsTesting(false);
		}
	}, [dbFormData]);

	const handleDbSave = async () => {
		if (!dbHasChanges || dbIsSaving) return;
		setDbIsSaving(true);
		try {
			await apiClient.updateDatabaseConfig({
				type: dbFormData.type,
				path: dbFormData.path,
				dsn: dbFormData.dsn,
			});
			const typeChanged = dbFormData.type !== config.database.type;
			setDbHasChanges(false);
			if (typeChanged) onRestartRequired?.("Database Backend");
			showToast({
				type: "success",
				title: "Database Saved",
				message: typeChanged
					? "Backend type changed — restart required to migrate data."
					: "Database settings saved.",
			});
		} catch (err) {
			showToast({
				type: "error",
				title: "Error",
				message: err instanceof Error ? err.message : "Failed to save database settings",
			});
		} finally {
			setDbIsSaving(false);
		}
	};

	const handleInputChange = (field: keyof LogFormData, value: string | number | boolean) => {
		const newData = { ...formData, [field]: value };
		setFormData(newData);
		const configData = {
			file: config.log.file,
			level: config.log.level,
			max_size: config.log.max_size,
			max_age: config.log.max_age,
			max_backups: config.log.max_backups,
			compress: config.log.compress,
		};
		setHasChanges(
			JSON.stringify(newData) !== JSON.stringify(configData) ||
				profilerEnabled !== config.profiler_enabled,
		);
	};

	const handleProfilerChange = (enabled: boolean) => {
		setProfilerEnabled(enabled);
		setHasChanges(true);
	};

	const handleSave = async () => {
		if (onUpdate && hasChanges) {
			// We need a way to update profiler_enabled too.
			// In ConfigurationPage, onUpdate for 'log' updates 'system' section which includes log.
			// Let's assume the backend handles both if we send them.
			await onUpdate("log", { ...formData, profiler_enabled: profilerEnabled } as LogFormData & {
				profiler_enabled: boolean;
			});
			setHasChanges(false);
		}
	};

	const handleCopyAPIKey = async () => {
		if (config.api_key) {
			const ok = await copyToClipboard(config.api_key);
			if (ok) {
				showToast({ type: "success", title: "Success", message: "API key copied to clipboard" });
			} else {
				showToast({ type: "error", title: "Error", message: "Failed to copy API key" });
			}
		}
	};

	const handleRegenerateAPIKey = async () => {
		const confirmed = await confirmAction(
			"Regenerate API Key",
			"This will generate a new API key and invalidate the current one. Continue?",
			{ type: "warning", confirmText: "Regenerate", confirmButtonClass: "btn-warning" },
		);
		if (confirmed) {
			try {
				await regenerateAPIKey.mutateAsync();
				if (onRefresh) await onRefresh();
				showToast({
					type: "success",
					title: "Success",
					message: "API key regenerated successfully",
				});
			} catch (_error) {
				showToast({ type: "error", title: "Error", message: "Failed to regenerate API key" });
			}
		}
	};

	const [activeTheme, setActiveTheme] = useState<ThemeId | "system">(getActiveTheme);

	const handleThemeChange = useCallback((theme: ThemeId | "system") => {
		setActiveTheme(theme);
		applyTheme(theme);
	}, []);

	return (
		<div className="min-w-0 space-y-10">
			<div className="min-w-0 space-y-8">
				{/* Updates */}
				<UpdateSection />

				{/* Appearance */}
				<div className="min-w-0 space-y-6 overflow-hidden rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="flex items-center gap-2">
						<Palette className="h-4 w-4 text-base-content/60" />
						<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
							Appearance
						</h4>
						<div className="h-px flex-1 bg-base-300/50" />
					</div>

					{/* System default */}
					<button
						type="button"
						className={`flex w-full items-center gap-3 rounded-lg border-2 p-3 text-left transition-all ${
							activeTheme === "system"
								? "border-primary bg-primary/5 ring-2 ring-primary/30"
								: "border-base-300 hover:border-primary/50"
						}`}
						onClick={() => handleThemeChange("system")}
					>
						<Monitor className="h-5 w-5 shrink-0 text-base-content/60" />
						<div className="min-w-0 flex-1">
							<span className="font-semibold text-sm">System Default</span>
							<p className="text-[11px] text-base-content/50">
								Follows your operating system&apos;s light/dark preference
							</p>
						</div>
						{activeTheme === "system" && <Check className="h-4 w-4 shrink-0 text-primary" />}
					</button>

					{/* Light themes */}
					<div>
						<p className="mb-2 font-semibold text-base-content/40 text-xs uppercase tracking-wider">
							Light
						</p>
						<div className="grid min-w-0 grid-cols-3 gap-2 sm:grid-cols-4 md:grid-cols-6">
							{LIGHT_THEMES.map((theme) => (
								<ThemeSwatch
									key={theme}
									theme={theme}
									isActive={activeTheme === theme}
									onClick={() => handleThemeChange(theme)}
								/>
							))}
						</div>
					</div>

					{/* Dark themes */}
					<div>
						<p className="mb-2 font-semibold text-base-content/40 text-xs uppercase tracking-wider">
							Dark
						</p>
						<div className="grid min-w-0 grid-cols-3 gap-2 sm:grid-cols-4 md:grid-cols-6">
							{DARK_THEMES.map((theme) => (
								<ThemeSwatch
									key={theme}
									theme={theme}
									isActive={activeTheme === theme}
									onClick={() => handleThemeChange(theme)}
								/>
							))}
						</div>
					</div>

					{/* Minimal themes */}
					<div>
						<p className="mb-2 font-semibold text-base-content/40 text-xs uppercase tracking-wider">
							Minimal
						</p>
						<div className="grid min-w-0 grid-cols-3 gap-2 sm:grid-cols-4 md:grid-cols-6">
							{MINIMAL_THEMES.map((theme) => (
								<ThemeSwatch
									key={theme}
									theme={theme}
									label={theme.replace("glass-", "")}
									minimal
									isActive={activeTheme === theme}
									onClick={() => handleThemeChange(theme)}
								/>
							))}
						</div>
					</div>
				</div>

				{/* Logging Configuration */}
				<div className="min-w-0 space-y-6 overflow-hidden rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="flex items-center gap-2">
						<Terminal className="h-4 w-4 text-base-content/60" />
						<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
							Diagnostics
						</h4>
						<div className="h-px flex-1 bg-base-300/50" />
					</div>

					<div className="grid min-w-0 grid-cols-1 gap-6 sm:grid-cols-2">
						<fieldset className="fieldset min-w-0">
							<legend className="fieldset-legend font-semibold text-xs">Minimum Log Level</legend>
							<select
								className="select select-bordered w-full min-w-0 max-w-full bg-base-100"
								value={formData.level}
								disabled={isReadOnly}
								onChange={(e) => handleInputChange("level", e.target.value)}
							>
								<option value="debug">Debug (Verbose)</option>
								<option value="info">Info (Standard)</option>
								<option value="warn">Warning (Alerts)</option>
								<option value="error">Error (Critical)</option>
							</select>
							<p className="label mt-2 min-w-0 max-w-full whitespace-normal break-words text-base-content/70 text-xs">
								Determines how much information is stored in logs.
							</p>
						</fieldset>

						<fieldset className="fieldset min-w-0">
							<legend className="fieldset-legend font-semibold text-xs">Max Log Size (MB)</legend>
							<input
								type="number"
								className="input input-bordered w-full min-w-0 max-w-full bg-base-100 font-mono text-sm"
								value={formData.max_size}
								disabled={isReadOnly}
								onChange={(e) =>
									handleInputChange("max_size", Number.parseInt(e.target.value, 10) || 0)
								}
							/>
						</fieldset>
					</div>

					<div className="grid min-w-0 grid-cols-1 gap-6 sm:grid-cols-3">
						<fieldset className="fieldset min-w-0">
							<legend className="fieldset-legend font-semibold text-xs">Max Age (Days)</legend>
							<input
								type="number"
								className="input input-bordered w-full min-w-0 max-w-full bg-base-100 font-mono text-sm"
								value={formData.max_age}
								disabled={isReadOnly}
								onChange={(e) =>
									handleInputChange("max_age", Number.parseInt(e.target.value, 10) || 0)
								}
							/>
						</fieldset>
						<fieldset className="fieldset min-w-0">
							<legend className="fieldset-legend font-semibold text-xs">Max Backups</legend>
							<input
								type="number"
								className="input input-bordered w-full min-w-0 max-w-full bg-base-100 font-mono text-sm"
								value={formData.max_backups}
								disabled={isReadOnly}
								onChange={(e) =>
									handleInputChange("max_backups", Number.parseInt(e.target.value, 10) || 0)
								}
							/>
						</fieldset>
						<fieldset className="fieldset min-w-0">
							<legend className="fieldset-legend font-semibold text-xs">Compress Logs</legend>
							<div className="flex h-12 items-center">
								<input
									type="checkbox"
									className="checkbox checkbox-primary"
									checked={formData.compress}
									disabled={isReadOnly}
									onChange={(e) => handleInputChange("compress", e.target.checked)}
								/>
							</div>
						</fieldset>
					</div>
				</div>

				{/* Database Storage */}
				<div className="min-w-0 space-y-6 overflow-hidden rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="flex items-center gap-2">
						<HardDrive className="h-4 w-4 text-base-content/60" />
						<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
							Database Storage
						</h4>
						<div className="h-px flex-1 bg-base-300/50" />
					</div>

					<div className="space-y-6">
						<div className="grid grid-cols-1 gap-6 md:grid-cols-2">
							<fieldset className="fieldset min-w-0">
								<legend className="fieldset-legend font-semibold text-xs">Database Type</legend>
								<select
									className="select select-bordered w-full min-w-0 max-w-full bg-base-100"
									value={dbFormData.type || "sqlite"}
									disabled={isReadOnly}
									onChange={(e) => handleDbChange("type", e.target.value)}
								>
									<option value="sqlite">SQLite (Default)</option>
									<option value="postgres">PostgreSQL</option>
								</select>
							</fieldset>

							{dbFormData.type === "postgres" ? (
								<fieldset className="fieldset min-w-0">
									<legend className="fieldset-legend font-semibold text-xs">
										Connection DSN
									</legend>
									<input
										type="text"
										className="input input-bordered w-full min-w-0 max-w-full bg-base-100 font-mono text-sm"
										value={dbFormData.dsn}
										readOnly={isReadOnly}
										placeholder="postgres://user:pass@host:5432/altmount?sslmode=disable"
										onChange={(e) => handleDbChange("dsn", e.target.value)}
									/>
								</fieldset>
							) : (
								<fieldset className="fieldset min-w-0">
									<legend className="fieldset-legend font-semibold text-xs">Database Path</legend>
									<input
										type="text"
										className="input input-bordered w-full min-w-0 max-w-full bg-base-100 font-mono text-sm"
										value={dbFormData.path}
										readOnly={isReadOnly}
										placeholder="/config/altmount.db"
										onChange={(e) => handleDbChange("path", e.target.value)}
									/>
								</fieldset>
							)}
						</div>

						{!isReadOnly && (
							<div className="flex flex-col gap-3">
								<div className="flex flex-wrap items-center gap-3">
									<button
										type="button"
										className="btn btn-outline btn-sm"
										onClick={handleDbTest}
										disabled={
											dbIsTesting ||
											(dbFormData.type === "postgres" && !dbFormData.dsn)
										}
									>
										{dbIsTesting ? (
											<LoadingSpinner size="sm" />
										) : (
											<Wifi className="h-4 w-4" />
										)}
										{dbIsTesting ? "Testing…" : "Test Connection"}
									</button>

									{dbHasChanges && (
										<button
											type="button"
											className="btn btn-primary btn-sm"
											onClick={handleDbSave}
											disabled={dbIsSaving}
										>
											{dbIsSaving ? (
												<LoadingSpinner size="sm" />
											) : (
												<Save className="h-4 w-4" />
											)}
											{dbIsSaving ? "Saving…" : "Save Database"}
										</button>
									)}
								</div>

								{dbTestResult && (
									<div
										className={`alert py-2 text-sm ${
											dbTestResult.status === "ok"
												? "alert-success"
												: dbTestResult.status === "new"
												  ? "alert-warning"
												  : "alert-error"
										}`}
									>
										<span>{dbTestResult.message}</span>
									</div>
								)}

								{dbHasChanges && dbFormData.type !== config.database.type && (
									<div className="alert alert-warning py-2 text-sm">
										<AlertTriangle className="h-4 w-4 shrink-0" />
										<span>
											Switching from{" "}
											<strong>{config.database.type || "sqlite"}</strong> to{" "}
											<strong>{dbFormData.type}</strong> requires a restart and
											will trigger an automatic data migration.
										</span>
									</div>
								)}
							</div>
						)}
					</div>
				</div>

				{/* Performance Profiler */}
				<div className="min-w-0 space-y-6 overflow-hidden rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="flex items-center gap-2">
						<Terminal className="h-4 w-4 text-base-content/60" />
						<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
							Performance
						</h4>
						<div className="h-px flex-1 bg-base-300/50" />
					</div>

					<div className="flex min-w-0 items-start justify-between gap-4">
						<div className="min-w-0 flex-1">
							<h5 className="font-bold text-sm">System Profiler (pprof)</h5>
							<p className="mt-1 break-words text-[11px] text-base-content/50 leading-relaxed">
								Enable Go runtime profiling at <code>/debug/pprof</code>. Only recommended for
								debugging resource leaks.
							</p>
						</div>
						<input
							type="checkbox"
							className="toggle toggle-warning mt-1 shrink-0"
							checked={profilerEnabled}
							disabled={isReadOnly}
							onChange={(e) => handleProfilerChange(e.target.checked)}
						/>
					</div>
				</div>

				{/* Security Section */}
				<div className="min-w-0 space-y-6 overflow-hidden rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="flex items-center gap-2">
						<ShieldCheck className="h-4 w-4 text-base-content/60" />
						<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
							Access Identity
						</h4>
						<div className="h-px flex-1 bg-base-300/50" />
					</div>

					<div className="space-y-6">
						<div className="min-w-0">
							<h5 className="font-bold text-sm">AltMount API Key</h5>
							<p className="mt-1 break-words text-[11px] text-base-content/50 leading-relaxed">
								Your personal secret key for authenticating external applications.
							</p>
						</div>

						<div className="flex flex-col gap-4">
							<div className="join w-full min-w-0 max-w-lg shadow-sm">
								<input
									type="text"
									className="input input-bordered join-item min-w-0 flex-1 overflow-hidden bg-base-100 font-mono text-xs"
									value={config.api_key || "Not Generated"}
									readOnly
								/>
								{config.api_key && (
									<button
										type="button"
										className="btn btn-ghost join-item border-base-300 px-4"
										onClick={handleCopyAPIKey}
									>
										<Copy className="h-4 w-4" />
									</button>
								)}
							</div>

							<div className="flex justify-start">
								<button
									type="button"
									className="btn btn-ghost btn-sm border-base-300 bg-base-100 hover:bg-base-200"
									onClick={handleRegenerateAPIKey}
									disabled={regenerateAPIKey.isPending}
								>
									{regenerateAPIKey.isPending ? (
										<LoadingSpinner size="sm" />
									) : (
										<RefreshCw className="h-3 w-3" />
									)}
									Regenerate Token
								</button>
							</div>
						</div>
					</div>
				</div>
			</div>

			{/* Save Button */}
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
