import { z } from 'zod';
import { createOperation } from '@wundergraph/sdk';

export default createOperation.query({
	input: z.object({
		a: z.number(),
		b: z.number(),
	}),
	handler: async (input) => {
		return {
			add: input.a + input.b,
		};
	},
});
