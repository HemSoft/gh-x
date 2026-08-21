import os from "node:os";
import path from "node:path";
import { access } from "node:fs/promises";
import { pathToFileURL } from "node:url";

export function resolveCopilotDashboardDirectory({
    env = process.env,
    homedir = os.homedir,
} = {}) {
    const copilotHome = env.COPILOT_HOME || path.join(homedir(), ".copilot");
    return path.join(copilotHome, "extensions", "copilot-spend");
}

export function createCopilotAdapter({
    extensionDirectory = resolveCopilotDashboardDirectory(),
    now = () => new Date(),
    importModule = (filePath) => import(pathToFileURL(filePath).href),
    accessImpl = access,
} = {}) {
    let modulesPromise;

    const loadModules = async () => {
        if (!modulesPromise) {
            modulesPromise = Promise.all([
                "session-store.mjs",
                "session-activity.mjs",
                "plan-usage.mjs",
                "account-quota-cache.mjs",
            ].map(async (name) => {
                const filePath = path.join(extensionDirectory, name);
                await accessImpl(filePath);
                return importModule(filePath);
            })).then(([sessionStore, sessionActivity, planUsage, accountQuotaCache]) => ({
                sessionStore,
                sessionActivity,
                planUsage,
                accountQuotaCache,
            }));
        }
        return modulesPromise;
    };

    return {
        extensionDirectory,
        async getSnapshot(filter = {}) {
            try {
                const {
                    sessionStore,
                    sessionActivity,
                    planUsage,
                    accountQuotaCache,
                } = await loadModules();
                const quotaCache = accountQuotaCache.createAccountQuotaCache({ now });
                const [snapshot, accountQuota] = await Promise.all([
                    sessionStore.readSessionStoreSnapshot({
                        now,
                        ...(filter.recentWindowMs === undefined
                            ? {}
                            : { recentWindowMs: filter.recentWindowMs }),
                        ...(filter.recentSince === undefined
                            ? {}
                            : { recentSince: filter.recentSince }),
                    }),
                    quotaCache.read(),
                ]);
                const activityReader = sessionActivity.createSessionActivityRegistry({
                    sessionId: "dashboard-hub-read-only",
                    now,
                });
                const activitySnapshot = await activityReader.overlay(snapshot);
                return {
                    ...activitySnapshot,
                    plan: planUsage.buildPlanUsage({
                        accountQuota,
                        billingPeriod: snapshot.billingPeriod,
                        now: now(),
                    }),
                };
            } catch (error) {
                return unavailableSnapshot(error, now());
            }
        },
    };
}

function unavailableSnapshot(error, refreshedAt) {
    return {
        available: false,
        source: "copilot-local",
        lastRefreshAt: refreshedAt.toISOString(),
        windows: null,
        totals: null,
        sessions: [],
        plan: {
            available: false,
            source: "unavailable",
            diagnostics: {
                code: "copilot_dashboard_unavailable",
                message: "Copilot plan usage is unavailable.",
            },
        },
        diagnostics: {
            code: error?.code || "copilot_dashboard_unavailable",
            message: error?.message || "Unable to read Copilot session data.",
        },
    };
}
