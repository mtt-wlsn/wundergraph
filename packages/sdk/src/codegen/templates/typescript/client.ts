import Handlebars from 'handlebars';
import hash from 'object-hash';
import { compile as compileJSONSchema } from 'json-schema-to-typescript';

import { handlebarTemplate } from './client.template';
import { Template, TemplateOutputFile } from '../../index';
import { formatTypeScript } from './';
import { OperationType } from '@wundergraph/protobuf';
import { ResolvedWunderGraphConfig } from '../../../configure';
import { liveQueries, modelImports, operations, queries as allQueries } from './helpers';
import templates from '../index';

export class TypeScriptClient implements Template {
	constructor(reactNative: boolean = false) {}
	async generate(config: ResolvedWunderGraphConfig): Promise<TemplateOutputFile[]> {
		const tmpl = Handlebars.compile(handlebarTemplate);
		const allOperations = allQueries(config.application, false);
		const _liveQueries = liveQueries(config.application, false);
		const _queries = operations(config.application, OperationType.QUERY, false);
		const _mutations = operations(config.application, OperationType.MUTATION, false);
		const _subscriptions = operations(config.application, OperationType.SUBSCRIPTION, false);
		const _uploadProfileTypeDefinitions: string[] = [];
		const _uploadProfileTypeNames: Record<string, Record<string, string>> = {};
		for (const provider of config.application.S3UploadProvider) {
			_uploadProfileTypeNames[provider.name] = {};
			for (const key in provider.uploadProfiles) {
				const profile = provider.uploadProfiles[key];
				if (profile.meta) {
					const requestedTypeName = `${provider.name}_${key}_metadata`;
					const typeDefinition = await compileJSONSchema(profile.meta, requestedTypeName, {
						additionalProperties: false,
						format: false,
						bannerComment: '',
					});
					// compileJSONSchema might change the typeName capitalization, retrieve it
					const match = typeDefinition.match(/export interface (\w+)/);
					if (!match) {
						throw new Error(`could not retrieve type name from ${typeDefinition}`);
					}
					const typeName = match[1];
					_uploadProfileTypeDefinitions.push(typeDefinition);
					_uploadProfileTypeNames[provider.name][key] = typeName;
				} else {
					_uploadProfileTypeNames[provider.name][key] = 'any | undefined';
				}
			}
		}
		const content = tmpl({
			modelImports: modelImports(config.application, false, true),
			baseURL: config.deployment.environment.baseUrl,
			roleDefinitions: config.authentication.roles.map((role) => '"' + role + '"').join(' | '),
			sdkVersion: config.sdkVersion,
			applicationHash: hash(config).substring(0, 8),
			queries: _queries,
			allOperations: allOperations,
			liveQueries: _liveQueries,
			hasLiveQueries: _liveQueries.length !== 0,
			hasOperations: allOperations.length !== 0,
			mutations: _mutations,
			hasMutations: _mutations.length !== 0,
			subscriptions: _subscriptions,
			hasSubscriptions: _subscriptions.length !== 0,
			hasSubscriptionsOrLiveQueries: _subscriptions.length + _liveQueries.length !== 0,
			hasAuthProviders: config.authentication.cookieBased.length !== 0,
			authProviders: config.authentication.cookieBased.map((provider) => provider.id),
			hasS3Providers: config.application.S3UploadProvider.length !== 0,
			s3Providers: config.application.S3UploadProvider,
			hasS3Provider: config.application.S3UploadProvider.length > 0,
			s3Provider: config.application.S3UploadProvider,
			uploadProfileTypeDefinitions: _uploadProfileTypeDefinitions,
			uploadProfileTypeNames: _uploadProfileTypeNames,
		});
		return Promise.resolve([
			{
				path: 'client.ts',
				content: formatTypeScript(content),
				doNotEditHeader: true,
			},
		]);
	}
	dependencies(): Template[] {
		return templates.typescript.models;
	}
}
