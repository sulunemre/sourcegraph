import {User} from "sourcegraph/api";
import {setGlobalFeatures} from "sourcegraph/app/features";
import {Features} from "sourcegraph/app/features";
import {setGlobalSiteConfig} from "sourcegraph/app/siteConfig";
import {UserStore} from "sourcegraph/user/UserStore";

class Context {
	xhrHeaders: {[key: string]: string};
	userAgentIsBot: boolean;
	user: User | null;
	intercomHash: string;

	// Some fields were migrated to React context from this global context object. These
	// getters prevent you from accidentally accessing these fields in their old home,
	// on this object.
	get currentUser(): void {
		throw new Error("currentUser is now accessible via this.context.user in components that specify 'user' in contextTypes");
	}
	get userEmail(): void {
		throw new Error("userEmail is no longer available globally; use the UserBackend/UserStore to retrieve it");
	}
	get hasLinkedGitHub(): void {
		throw new Error("hasLinkedGitHub is no longer available globally; use the UserBackend/UserStore directly");
	}
}

export const context = new Context();

// ContextInput is the input context to set up the JS environment (e.g., from Go).
type ContextInput = typeof context & {
	// We are migrating from a global context object to using React context
	// as much as possible. These fields are only available using context wrappers.
	appURL?: string;
	assetsRoot?: string;
	buildVars?: {Version: string};
	features?: Features;

	// This is now available in UserStore.activeAccessToken.
	accessToken?: string;
};

// Sets the values of the context given a JSContext object from the server.
export function reset(ctx: ContextInput): void {
	const features = ctx.features;
	delete ctx.features;
	if (typeof features !== "undefined") {
		setGlobalFeatures(features);
	}

	const {appURL, assetsRoot, buildVars} = ctx;
	if (typeof appURL === "undefined" || typeof assetsRoot === "undefined" || typeof buildVars === "undefined") {
		throw new Error("appURL, assetsRoot, and buildVars must all be set");
	}
	setGlobalSiteConfig({appURL, assetsRoot, buildVars});
	delete ctx.appURL;
	delete ctx.assetsRoot;
	delete ctx.buildVars;

	if (ctx.accessToken) {
		UserStore.activeAccessToken = ctx.accessToken;
	}
	delete ctx.accessToken;

	Object.assign(context, ctx);
}
