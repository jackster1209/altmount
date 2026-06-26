import { QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { useEffect, useState } from "react";
import { BrowserRouter, Route, Routes } from "react-router-dom";
import { ProtectedRoute } from "./components/auth/ProtectedRoute";
import { Layout } from "./components/layout/Layout";
import { PWAUpdatePrompt } from "./components/ui/PWAUpdatePrompt";
import { ToastContainer } from "./components/ui/ToastContainer";
import { AuthProvider } from "./contexts/AuthContext";
import { ModalProvider } from "./contexts/ModalContext";
import { ToastProvider } from "./contexts/ToastContext";
import { queryClient } from "./lib/queryClient";
import { ConfigurationPage } from "./pages/ConfigurationPage";
import { Dashboard } from "./pages/Dashboard";
import { FilesPage } from "./pages/FilesPage";
import { HealthPage } from "./pages/HealthPage";
import { LogsPage } from "./pages/LogsPage";
import { MaintenancePage } from "./pages/MaintenancePage";
import { QueuePage } from "./pages/QueuePage";

function MaintenanceModeGate({ children }: { children: React.ReactNode }) {
	const [checking, setChecking] = useState(true);
	const [isMaintenance, setIsMaintenance] = useState(false);

	useEffect(() => {
		fetch("/api/maintenance/status", { cache: "no-store" })
			.then(async (res) => {
				if (!res.ok) {
					setIsMaintenance(false);
					return;
				}
				const data = await res.json().catch(() => ({}));
				setIsMaintenance(data?.mode === "maintenance");
			})
			.catch(() => setIsMaintenance(false))
			.finally(() => setChecking(false));
	}, []);

	if (checking) {
		return (
			<div className="flex min-h-screen items-center justify-center bg-base-100">
				<span className="loading loading-spinner loading-lg" />
			</div>
		);
	}

	if (isMaintenance) {
		return <MaintenancePage />;
	}

	return <>{children}</>;
}

function App() {
	return (
		<MaintenanceModeGate>
			<QueryClientProvider client={queryClient}>
				<ToastProvider>
					<ModalProvider>
						<AuthProvider>
							<BrowserRouter>
								<div className="min-h-screen bg-base-100">
									<Routes>
										{/* Protected routes */}
										<Route
											path="/"
											element={
												<ProtectedRoute>
													<Layout />
												</ProtectedRoute>
											}
										>
											<Route index element={<Dashboard />} />
											<Route path="queue" element={<QueuePage />} />
											<Route path="health" element={<HealthPage />} />
											<Route path="health/:tab" element={<HealthPage />} />
											<Route path="files" element={<FilesPage />} />
											<Route path="logs" element={<LogsPage />} />

											{/* Admin-only routes */}
											<Route
												path="config"
												element={
													<ProtectedRoute requireAdmin>
														<ConfigurationPage />
													</ProtectedRoute>
												}
											/>
											<Route
												path="config/:section"
												element={
													<ProtectedRoute requireAdmin>
														<ConfigurationPage />
													</ProtectedRoute>
												}
											/>
										</Route>
									</Routes>
								</div>
								<ToastContainer />
								<PWAUpdatePrompt />
							</BrowserRouter>
						</AuthProvider>
					</ModalProvider>
				</ToastProvider>
				<ReactQueryDevtools initialIsOpen={false} />
			</QueryClientProvider>
		</MaintenanceModeGate>
	);
}

export default App;
